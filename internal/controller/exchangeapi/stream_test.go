package exchangeapi

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
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/fengqi-dev/kube-loop/internal/controller"
	"github.com/fengqi-dev/kube-loop/internal/controller/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controller/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/exchangestream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/servicebinding"
	"github.com/google/uuid"
)

type recordingExchangeResources struct {
	store Storage

	mu         sync.Mutex
	events     []string
	snapshot   servicebinding.ServiceInterceptSnapshot
	applyErr   error
	restoreErr error
}

func (resources *recordingExchangeResources) Capture(
	_ context.Context,
	_ controller.Principal,
	snapshot *servicebinding.ServiceInterceptSnapshot,
) error {
	resources.mu.Lock()
	defer resources.mu.Unlock()
	resources.events = append(resources.events, "capture")
	snapshot.Selector = map[string]string{"app": "api"}
	return nil
}

func (resources *recordingExchangeResources) Apply(
	ctx context.Context,
	_ controller.Principal,
	snapshot servicebinding.ServiceInterceptSnapshot,
	taskID string,
) error {
	stored, err := resources.store.ResourceSnapshots().ListByTask(ctx, taskID)
	if err != nil || len(stored) != 1 {
		return errors.New("rollback snapshot was not durable before Apply")
	}
	resources.mu.Lock()
	defer resources.mu.Unlock()
	resources.events = append(resources.events, "apply")
	resources.snapshot = snapshot
	return resources.applyErr
}

func (resources *recordingExchangeResources) Restore(
	_ context.Context,
	snapshot servicebinding.ServiceInterceptSnapshot,
	_ string,
) error {
	resources.mu.Lock()
	defer resources.mu.Unlock()
	resources.events = append(resources.events, "restore")
	resources.snapshot = snapshot
	return resources.restoreErr
}

func (resources *recordingExchangeResources) state() ([]string, servicebinding.ServiceInterceptSnapshot) {
	resources.mu.Lock()
	defer resources.mu.Unlock()
	return append([]string(nil), resources.events...), resources.snapshot
}

func (resources *recordingExchangeResources) failRestore(err error) {
	resources.mu.Lock()
	defer resources.mu.Unlock()
	resources.restoreErr = err
}

func TestExchangeStreamRelaysTCPAndUDPThenRestores(t *testing.T) {
	stateStore, principal, active := exchangeStreamStore(t)
	resources := &recordingExchangeResources{store: stateStore}
	services := &exchangeTestServices{}
	handler, err := New(
		stateStore, exchangeTestSessions{session: active}, services, resources,
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
	task := createExchangeTask(t, handler, principal, active.ID)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if apiError := handler.ServeAPI(writer, request, principal); apiError != nil {
			http.Error(writer, apiError.Message, apiErrorStatus(apiError))
		}
	}))
	t.Cleanup(server.Close)
	streamURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v2/sessions/" + active.ID +
		"/exchanges/" + task.ID + "/stream?namespace=development"
	connection, response, err := websocket.Dial(context.Background(), streamURL, nil)
	if err != nil {
		if response != nil {
			t.Fatalf("dial Exchange stream: %v status=%d", err, response.StatusCode)
		}
		t.Fatal(err)
	}
	defer connection.CloseNow()
	frame := readExchangeFrame(t, connection)
	if frame.Type != exchangestream.Ready {
		t.Fatalf("first Exchange frame=%#v", frame)
	}
	_, snapshot := resources.state()
	if len(snapshot.Ports) != 2 {
		t.Fatalf("captured listener snapshot=%#v", snapshot)
	}
	tcpPort, udpPort := listenerPorts(t, snapshot)

	tcpConnection, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", tcpPort))
	if err != nil {
		t.Fatal(err)
	}
	defer tcpConnection.Close()
	open := readExchangeFrame(t, connection)
	if open.Type != exchangestream.Open || open.ServicePort != 80 || open.Protocol != exchangestream.ProtocolTCP {
		t.Fatalf("TCP open frame=%#v", open)
	}
	if _, err := tcpConnection.Write([]byte("cluster-request")); err != nil {
		t.Fatal(err)
	}
	data := readExchangeFrame(t, connection)
	if data.Type != exchangestream.Data || data.StreamID != open.StreamID || !bytes.Equal(data.Payload, []byte("cluster-request")) {
		t.Fatalf("TCP request frame=%#v", data)
	}
	writeExchangeFrame(t, connection, exchangestream.Frame{
		Type: exchangestream.Data, StreamID: open.StreamID, Payload: []byte("local-response"),
	})
	responseBytes := make([]byte, len("local-response"))
	if _, err := io.ReadFull(tcpConnection, responseBytes); err != nil || string(responseBytes) != "local-response" {
		t.Fatalf("TCP response=%q err=%v", responseBytes, err)
	}
	if err := tcpConnection.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	halfClose := readExchangeFrame(t, connection)
	if halfClose.Type != exchangestream.CloseWrite || halfClose.StreamID != open.StreamID {
		t.Fatalf("TCP half-close frame=%#v", halfClose)
	}
	writeExchangeFrame(t, connection, exchangestream.Frame{Type: exchangestream.CloseWrite, StreamID: open.StreamID})
	_ = tcpConnection.SetReadDeadline(time.Now().Add(time.Second))
	buffer := make([]byte, 1)
	if _, err := tcpConnection.Read(buffer); !errors.Is(err, io.EOF) {
		t.Fatalf("TCP client half-close did not reach cluster peer: %v", err)
	}
	writeExchangeFrame(t, connection, exchangestream.Frame{Type: exchangestream.Close, StreamID: open.StreamID})

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
	datagram := readExchangeFrame(t, connection)
	if datagram.Type != exchangestream.Datagram || datagram.ServicePort != 53 ||
		datagram.Protocol != exchangestream.ProtocolUDP || string(datagram.Payload) != "udp-request" {
		t.Fatalf("UDP request frame=%#v", datagram)
	}
	writeExchangeFrame(t, connection, exchangestream.Frame{
		Type: exchangestream.Datagram, StreamID: datagram.StreamID, ServicePort: 53,
		Protocol: exchangestream.ProtocolUDP, Payload: []byte("udp-response"),
	})
	_ = udpConnection.SetReadDeadline(time.Now().Add(time.Second))
	udpResponse := make([]byte, 64)
	count, err := udpConnection.Read(udpResponse)
	if err != nil || string(udpResponse[:count]) != "udp-response" {
		t.Fatalf("UDP response=%q err=%v", udpResponse[:count], err)
	}

	writeExchangeFrame(t, connection, exchangestream.Frame{Type: exchangestream.Stop})
	for {
		frame, readErr := readExchangeFrameWithError(connection)
		if readErr != nil {
			break
		}
		if frame.Type == exchangestream.Stop {
			break
		}
	}
	waitForExchangeState(t, stateStore, task.ID, "stopped")
	events, _ := resources.state()
	if strings.Join(events, ",") != "capture,apply,restore" {
		t.Fatalf("resource lifecycle=%v", events)
	}
	snapshots, err := stateStore.ResourceSnapshots().ListByTask(context.Background(), task.ID)
	if err != nil || len(snapshots) != 0 {
		t.Fatalf("completed Exchange snapshots=%#v err=%v", snapshots, err)
	}
}

func TestExchangeStreamRestoresOnStopTokenRevocationAndSessionEnd(t *testing.T) {
	for _, scenario := range []string{"durable-stop", "token-revocation", "session-end"} {
		t.Run(scenario, func(t *testing.T) {
			stateStore, principal, active := exchangeStreamStore(t)
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
			resources := &recordingExchangeResources{store: stateStore}
			handler, err := New(
				stateStore, exchangeTestSessions{session: active}, &exchangeTestServices{}, resources,
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
			task := createExchangeTask(t, handler, principal, active.ID)
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if apiError := handler.ServeAPI(writer, request, principal); apiError != nil {
					http.Error(writer, apiError.Message, apiErrorStatus(apiError))
				}
			}))
			defer server.Close()
			streamURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v2/sessions/" + active.ID +
				"/exchanges/" + task.ID + "/stream?namespace=development"
			connection, _, err := websocket.Dial(context.Background(), streamURL, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer connection.CloseNow()
			if frame := readExchangeFrame(t, connection); frame.Type != exchangestream.Ready {
				t.Fatalf("ready frame=%#v", frame)
			}
			switch scenario {
			case "durable-stop":
				path := "/api/v2/sessions/" + active.ID + "/exchanges/" + task.ID + "?namespace=development"
				response, apiError := exchangeRequest(handler, principal, http.MethodDelete, path, nil, "")
				if apiError != nil || response.Code != http.StatusAccepted {
					t.Fatalf("stop response=%d error=%#v", response.Code, apiError)
				}
			case "token-revocation":
				if err := stateStore.TokenFamilies().Revoke(context.Background(), principal.FamilyID, time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
			case "session-end":
				if err := stateStore.Sessions().UpdateState(context.Background(), active.ID, active.Generation, "stopped", time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
			}
			for {
				frame, readErr := readExchangeFrameWithError(connection)
				if readErr != nil || frame.Type == exchangestream.Stop {
					break
				}
			}
			waitForExchangeState(t, stateStore, task.ID, "stopped")
			events, _ := resources.state()
			if strings.Join(events, ",") != "capture,apply,restore" {
				t.Fatalf("resource lifecycle=%v", events)
			}
		})
	}
}

func TestExchangeStreamRestoreFailureStaysRecoverable(t *testing.T) {
	stateStore, principal, active := exchangeStreamStore(t)
	resources := &recordingExchangeResources{store: stateStore}
	handler, err := New(
		stateStore, exchangeTestSessions{session: active}, &exchangeTestServices{}, resources,
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
	task := createExchangeTask(t, handler, principal, active.ID)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if apiError := handler.ServeAPI(writer, request, principal); apiError != nil {
			http.Error(writer, apiError.Message, apiErrorStatus(apiError))
		}
	}))
	defer server.Close()
	streamURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v2/sessions/" + active.ID +
		"/exchanges/" + task.ID + "/stream?namespace=development"
	connection, _, err := websocket.Dial(context.Background(), streamURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	if frame := readExchangeFrame(t, connection); frame.Type != exchangestream.Ready {
		t.Fatalf("ready frame=%#v", frame)
	}
	resources.failRestore(errors.New("simulated restore outage"))
	writeExchangeFrame(t, connection, exchangestream.Frame{Type: exchangestream.Stop})
	waitForExchangeState(t, stateStore, task.ID, "recovering")
	snapshots, err := stateStore.ResourceSnapshots().ListByTask(context.Background(), task.ID)
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("recoverable Exchange snapshots=%#v err=%v", snapshots, err)
	}
}

func exchangeStreamStore(t *testing.T) (*storage.Store, controller.Principal, sessionapi.ActiveSession) {
	t.Helper()
	ctx := context.Background()
	stateStore, err := storage.Open(ctx, storage.Config{Backend: storage.BackendSQLite, SQLitePath: t.TempDir() + "/exchange.db"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stateStore.Close() })
	now := time.Now().UTC()
	principalID, sessionID := uuid.NewString(), uuid.NewString()
	if _, err := stateStore.Principals().Upsert(ctx, storage.Principal{
		ID: principalID, Provider: "test", ExternalID: "exchange-stream-user", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	network, err := networkspec.Normalize(networkspec.Spec{ServiceIPs: []string{"10.96.0.10"}})
	if err != nil {
		t.Fatal(err)
	}
	networkJSON, _ := networkspec.CanonicalJSON(network)
	networkHash, _ := networkspec.Hash(network)
	expiresAt := now.Add(time.Hour)
	if err := stateStore.Sessions().Create(ctx, storage.Session{
		ID: sessionID, PrincipalID: principalID, DeviceID: "device", ClusterID: "cluster",
		Namespace: "development", State: "active", Generation: 1,
		NetworkSpec: networkJSON, NetworkSpecHash: networkHash,
		CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: expiresAt,
	}); err != nil {
		t.Fatal(err)
	}
	return stateStore,
		controller.Principal{Subject: principalID, DeviceID: "device"},
		sessionapi.ActiveSession{ID: sessionID, Namespace: "development", Generation: 1, ExpiresAt: expiresAt}
}

func createExchangeTask(t *testing.T, handler *Handler, principal controller.Principal, sessionID string) Document {
	t.Helper()
	path := "/api/v2/sessions/" + sessionID + "/exchanges?namespace=development"
	body := []byte(`{"service":"api","ports":[{"servicePort":53,"protocol":"udp"},{"servicePort":80,"protocol":"tcp"}]}`)
	response, apiError := exchangeRequest(handler, principal, http.MethodPost, path, body, uuid.NewString())
	if apiError != nil || response.Code != http.StatusCreated {
		t.Fatalf("create Exchange: status=%d error=%#v body=%s", response.Code, apiError, response.Body.String())
	}
	var document Document
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	return document
}

func readExchangeFrame(t *testing.T, connection *websocket.Conn) exchangestream.Frame {
	t.Helper()
	frame, err := readExchangeFrameWithError(connection)
	if err != nil {
		t.Fatal(err)
	}
	return frame
}

func readExchangeFrameWithError(connection *websocket.Conn) (exchangestream.Frame, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	messageType, encoded, err := connection.Read(ctx)
	if err != nil {
		return exchangestream.Frame{}, err
	}
	if messageType != websocket.MessageBinary {
		return exchangestream.Frame{}, errors.New("expected binary Exchange frame")
	}
	return exchangestream.Decode(encoded)
}

func writeExchangeFrame(t *testing.T, connection *websocket.Conn, frame exchangestream.Frame) {
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

func listenerPorts(t *testing.T, snapshot servicebinding.ServiceInterceptSnapshot) (string, string) {
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
		t.Fatalf("listener mappings=%#v", snapshot.Ports)
	}
	return tcpPort, udpPort
}

func waitForExchangeState(t *testing.T, stateStore *storage.Store, taskID, state string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		task, err := stateStore.Tasks().GetByID(context.Background(), taskID)
		if err == nil && string(task.State) == state {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	task, err := stateStore.Tasks().GetByID(context.Background(), taskID)
	t.Fatalf("Exchange Task state=%q want=%q err=%v", task.State, state, err)
}

func apiErrorStatus(apiError *controller.APIError) int {
	if apiError.Code == controller.CodeConflict {
		return http.StatusConflict
	}
	return http.StatusBadRequest
}
