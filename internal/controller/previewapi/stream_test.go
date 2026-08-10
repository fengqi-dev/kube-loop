package previewapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/fengqi-dev/kube-loop/internal/controller"
	"github.com/fengqi-dev/kube-loop/internal/controller/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/exchangestream"
	"github.com/fengqi-dev/kube-loop/internal/servicebinding"
	"github.com/google/uuid"
)

func TestPreviewStreamRelaysTCPAndUDPAndDeletesOwnedResources(t *testing.T) {
	stateStore, principal, active := previewTestStore(t)
	resources := &recordingPreviewResources{}
	handler, err := New(
		stateStore, previewTestSessions{session: active}, resources,
		Config{
			GatewayIP: "127.0.0.1", OwnerID: "controller-test",
			CredentialCheckInterval: 20 * time.Millisecond,
			TaskCheckInterval:       20 * time.Millisecond,
			UDPIdleTimeout:          time.Second,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	task := createPreviewTask(t, handler, principal, active.ID)
	server := previewTestServer(t, handler, principal)
	defer server.Close()
	connection, _, err := websocket.Dial(
		context.Background(),
		"ws"+strings.TrimPrefix(server.URL, "http")+"/api/v2/sessions/"+active.ID+
			"/previews/"+task.ID+"/stream?namespace=development",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	if frame := readPreviewFrame(t, connection); frame.Type != exchangestream.Ready {
		t.Fatalf("first Preview frame=%#v", frame)
	}
	snapshot, createdID, deletedID := resources.state()
	if createdID != task.ID || deletedID != "" || len(snapshot.Ports) != 2 {
		t.Fatalf("created Preview resources snapshot=%#v created=%q deleted=%q", snapshot, createdID, deletedID)
	}
	tcpPort, udpPort := previewListenerPorts(t, snapshot)

	running, apiError := previewRequest(
		handler, principal, http.MethodGet,
		"/api/v2/sessions/"+active.ID+"/previews/"+task.ID+"?namespace=development", nil, "",
	)
	if apiError != nil {
		t.Fatal(apiError)
	}
	var runningDocument Document
	if err := json.Unmarshal(running.Body.Bytes(), &runningDocument); err != nil ||
		runningDocument.State != "running" || runningDocument.ClusterIP != "10.96.0.40" {
		t.Fatalf("running Preview=%#v err=%v", runningDocument, err)
	}

	tcpConnection, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", tcpPort))
	if err != nil {
		t.Fatal(err)
	}
	open := readPreviewUntil(t, connection, exchangestream.Open)
	if open.ServicePort != 80 || open.Protocol != exchangestream.ProtocolTCP {
		t.Fatalf("Preview TCP open=%#v", open)
	}
	if _, err := tcpConnection.Write([]byte("cluster-request")); err != nil {
		t.Fatal(err)
	}
	data := readPreviewUntil(t, connection, exchangestream.Data)
	if data.StreamID != open.StreamID || string(data.Payload) != "cluster-request" {
		t.Fatalf("Preview TCP data=%#v", data)
	}
	writePreviewFrame(t, connection, exchangestream.Frame{
		Type: exchangestream.Data, StreamID: open.StreamID, Payload: []byte("desktop-response"),
	})
	response := make([]byte, len("desktop-response"))
	_ = tcpConnection.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := io.ReadFull(tcpConnection, response); err != nil || string(response) != "desktop-response" {
		t.Fatalf("Preview TCP response=%q err=%v", response, err)
	}
	writePreviewFrame(t, connection, exchangestream.Frame{Type: exchangestream.Close, StreamID: open.StreamID})
	_ = tcpConnection.Close()

	udpAddress, err := net.ResolveUDPAddr("udp", net.JoinHostPort("127.0.0.1", udpPort))
	if err != nil {
		t.Fatal(err)
	}
	udpConnection, err := net.DialUDP("udp", nil, udpAddress)
	if err != nil {
		t.Fatal(err)
	}
	defer udpConnection.Close()
	if _, err := udpConnection.Write([]byte("udp-request")); err != nil {
		t.Fatal(err)
	}
	datagram := readPreviewUntil(t, connection, exchangestream.Datagram)
	if datagram.ServicePort != 53 || datagram.Protocol != exchangestream.ProtocolUDP || string(datagram.Payload) != "udp-request" {
		t.Fatalf("Preview UDP datagram=%#v", datagram)
	}
	writePreviewFrame(t, connection, exchangestream.Frame{
		Type: exchangestream.Datagram, StreamID: datagram.StreamID,
		ServicePort: datagram.ServicePort, Protocol: exchangestream.ProtocolUDP,
		Payload: []byte("udp-response"),
	})
	_ = udpConnection.SetReadDeadline(time.Now().Add(time.Second))
	udpResponse := make([]byte, 64)
	count, err := udpConnection.Read(udpResponse)
	if err != nil || string(udpResponse[:count]) != "udp-response" {
		t.Fatalf("Preview UDP response=%q err=%v", udpResponse[:count], err)
	}

	writePreviewFrame(t, connection, exchangestream.Frame{Type: exchangestream.Stop})
	waitForPreviewState(t, stateStore, task.ID, "stopped")
	_, createdID, deletedID = resources.state()
	if createdID != task.ID || deletedID != task.ID {
		t.Fatalf("Preview resource owner create=%q delete=%q", createdID, deletedID)
	}
	snapshots, err := stateStore.ResourceSnapshots().ListByTask(context.Background(), task.ID)
	if err != nil || len(snapshots) != 0 {
		t.Fatalf("Preview cleanup intents=%#v err=%v", snapshots, err)
	}
}

func TestPreviewDeleteFailureStaysRecoverable(t *testing.T) {
	stateStore, principal, active := previewTestStore(t)
	resources := &recordingPreviewResources{deleteErr: errors.New("simulated Kubernetes delete outage")}
	handler, err := New(
		stateStore, previewTestSessions{session: active}, resources,
		Config{
			GatewayIP: "127.0.0.1", OwnerID: "controller-test",
			CredentialCheckInterval: 20 * time.Millisecond,
			TaskCheckInterval:       20 * time.Millisecond,
			UDPIdleTimeout:          time.Second,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	task := createPreviewTask(t, handler, principal, active.ID)
	server := previewTestServer(t, handler, principal)
	defer server.Close()
	connection, _, err := websocket.Dial(
		context.Background(),
		"ws"+strings.TrimPrefix(server.URL, "http")+"/api/v2/sessions/"+active.ID+
			"/previews/"+task.ID+"/stream?namespace=development", nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	if frame := readPreviewFrame(t, connection); frame.Type != exchangestream.Ready {
		t.Fatalf("Preview ready=%#v", frame)
	}
	writePreviewFrame(t, connection, exchangestream.Frame{Type: exchangestream.Stop})
	waitForPreviewState(t, stateStore, task.ID, "recovering")
	snapshots, err := stateStore.ResourceSnapshots().ListByTask(context.Background(), task.ID)
	if err != nil || len(snapshots) != 1 || snapshots[0].Kind != previewSnapshotKind {
		t.Fatalf("recoverable Preview cleanup intent=%#v err=%v", snapshots, err)
	}
}

func TestPreviewStreamDeletesOwnedResourcesOnStopTokenRevocationAndSessionEnd(t *testing.T) {
	for _, scenario := range []string{"durable-stop", "token-revocation", "session-end"} {
		t.Run(scenario, func(t *testing.T) {
			stateStore, principal, active := previewTestStore(t)
			now := time.Now().UTC()
			if scenario == "token-revocation" {
				principal.FamilyID = uuid.NewString()
				if err := stateStore.TokenFamilies().Create(context.Background(), storage.TokenFamily{
					ID: principal.FamilyID, PrincipalID: principal.Subject, DeviceID: principal.DeviceID,
					RefreshTokenHash: bytes.Repeat([]byte{7}, 32), CreatedAt: now, ExpiresAt: now.Add(time.Hour),
				}); err != nil {
					t.Fatal(err)
				}
			}
			resources := &recordingPreviewResources{}
			handler, err := New(
				stateStore, previewTestSessions{session: active}, resources,
				Config{
					GatewayIP: "127.0.0.1", OwnerID: "controller-test",
					CredentialCheckInterval: 20 * time.Millisecond,
					TaskCheckInterval:       20 * time.Millisecond,
					UDPIdleTimeout:          time.Second,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			task := createPreviewTask(t, handler, principal, active.ID)
			server := previewTestServer(t, handler, principal)
			defer server.Close()
			connection, _, err := websocket.Dial(
				context.Background(),
				"ws"+strings.TrimPrefix(server.URL, "http")+"/api/v2/sessions/"+active.ID+
					"/previews/"+task.ID+"/stream?namespace=development", nil,
			)
			if err != nil {
				t.Fatal(err)
			}
			defer connection.CloseNow()
			if frame := readPreviewFrame(t, connection); frame.Type != exchangestream.Ready {
				t.Fatalf("Preview ready=%#v", frame)
			}
			switch scenario {
			case "durable-stop":
				path := "/api/v2/sessions/" + active.ID + "/previews/" + task.ID + "?namespace=development"
				response, apiError := previewRequest(handler, principal, http.MethodDelete, path, nil, "")
				if apiError != nil || response.Code != http.StatusAccepted {
					t.Fatalf("stop response=%d error=%#v", response.Code, apiError)
				}
			case "token-revocation":
				if err := stateStore.TokenFamilies().Revoke(context.Background(), principal.FamilyID, time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
			case "session-end":
				if err := stateStore.Sessions().UpdateState(
					context.Background(), active.ID, active.Generation, "stopped", time.Now().UTC(),
				); err != nil {
					t.Fatal(err)
				}
			}
			for {
				frame, readErr := readPreviewFrameWithError(connection)
				if readErr != nil || frame.Type == exchangestream.Stop {
					break
				}
			}
			waitForPreviewState(t, stateStore, task.ID, "stopped")
			_, createdID, deletedID := resources.state()
			if createdID != task.ID || deletedID != task.ID {
				t.Fatalf("Preview resource owner create=%q delete=%q", createdID, deletedID)
			}
			snapshots, err := stateStore.ResourceSnapshots().ListByTask(context.Background(), task.ID)
			if err != nil || len(snapshots) != 0 {
				t.Fatalf("completed Preview snapshots=%#v err=%v", snapshots, err)
			}
		})
	}
}

func createPreviewTask(t *testing.T, handler *Handler, principal controller.Principal, sessionID string) Document {
	t.Helper()
	response, apiError := previewRequest(
		handler, principal, http.MethodPost,
		"/api/v2/sessions/"+sessionID+"/previews?namespace=development",
		[]byte(`{"name":"local-api","ports":[{"servicePort":53,"protocol":"udp"},{"servicePort":80,"protocol":"tcp"}]}`),
		uuid.NewString(),
	)
	if apiError != nil || response.Code != http.StatusCreated {
		t.Fatalf("create Preview: status=%d error=%#v body=%s", response.Code, apiError, response.Body.String())
	}
	var document Document
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	return document
}

func previewTestServer(t *testing.T, handler *Handler, principal controller.Principal) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if apiError := handler.ServeAPI(writer, request, principal); apiError != nil {
			http.Error(writer, apiError.Message, previewAPIErrorStatus(apiError))
		}
	}))
}

func previewAPIErrorStatus(apiError *controller.APIError) int {
	switch apiError.Code {
	case controller.CodeInvalidArgument:
		return http.StatusBadRequest
	case controller.CodeUnauthenticated:
		return http.StatusUnauthorized
	case controller.CodeForbidden:
		return http.StatusForbidden
	case controller.CodeNotFound:
		return http.StatusNotFound
	case controller.CodeConflict:
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func readPreviewFrame(t *testing.T, connection *websocket.Conn) exchangestream.Frame {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	messageType, encoded, err := connection.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if messageType != websocket.MessageBinary {
		t.Fatal("expected binary Preview frame")
	}
	frame, err := exchangestream.Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return frame
}

func readPreviewFrameWithError(connection *websocket.Conn) (exchangestream.Frame, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	messageType, encoded, err := connection.Read(ctx)
	if err != nil {
		return exchangestream.Frame{}, err
	}
	if messageType != websocket.MessageBinary {
		return exchangestream.Frame{}, errors.New("expected binary Preview frame")
	}
	return exchangestream.Decode(encoded)
}

func readPreviewUntil(t *testing.T, connection *websocket.Conn, frameType byte) exchangestream.Frame {
	t.Helper()
	for {
		frame := readPreviewFrame(t, connection)
		if frame.Type == frameType {
			return frame
		}
	}
}

func writePreviewFrame(t *testing.T, connection *websocket.Conn, frame exchangestream.Frame) {
	t.Helper()
	encoded, err := exchangestream.Encode(frame)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := connection.Write(ctx, websocket.MessageBinary, encoded); err != nil {
		t.Fatal(err)
	}
}

func previewListenerPorts(t *testing.T, snapshot servicebinding.PreviewServiceSnapshot) (string, string) {
	t.Helper()
	var tcpPort, udpPort string
	for _, port := range snapshot.Ports {
		switch port.Protocol {
		case "TCP":
			tcpPort = strconv.Itoa(int(port.ListenPort))
		case "UDP":
			udpPort = strconv.Itoa(int(port.ListenPort))
		}
	}
	if tcpPort == "" || udpPort == "" {
		t.Fatalf("Preview listener mappings=%#v", snapshot.Ports)
	}
	return tcpPort, udpPort
}

func waitForPreviewState(t *testing.T, stateStore *storage.Store, taskID, want string) storage.Task {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		task, err := stateStore.Tasks().GetByID(context.Background(), taskID)
		if err == nil && string(task.State) == want {
			return task
		}
		select {
		case <-deadline.C:
			t.Fatalf("Preview Task %s did not reach %s: task=%#v err=%v", taskID, want, task, err)
		case <-ticker.C:
		}
	}
}
