package mirrorapi

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
	"github.com/fengqi-dev/kube-loop/internal/protocol/mirrorstream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/servicebinding"
	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
)

type recordingMirrorResources struct {
	store Storage

	mu         sync.Mutex
	events     []string
	snapshot   servicebinding.ServiceInterceptSnapshot
	applyErr   error
	restoreErr error
	tcpPort    int32
	udpPort    int32
}

func (resources *recordingMirrorResources) Capture(
	_ context.Context,
	_ controller.Principal,
	snapshot *servicebinding.ServiceInterceptSnapshot,
) error {
	resources.mu.Lock()
	defer resources.mu.Unlock()
	resources.events = append(resources.events, "capture")
	snapshot.Selector = map[string]string{"app": "api"}
	tcpPort, udpPort := resources.tcpPort, resources.udpPort
	if tcpPort == 0 {
		tcpPort = 1
	}
	if udpPort == 0 {
		udpPort = 1
	}
	snapshot.HasEndpointSlices = true
	snapshot.EndpointSlices = []discoveryv1.EndpointSlice{{
		AddressType: discoveryv1.AddressTypeIPv4,
		Endpoints:   []discoveryv1.Endpoint{{Addresses: []string{"127.0.0.1"}}},
		Ports: []discoveryv1.EndpointPort{
			{Protocol: new(corev1.ProtocolTCP), Port: &tcpPort},
			{Protocol: new(corev1.ProtocolUDP), Port: &udpPort},
		},
	}}
	return nil
}

func (resources *recordingMirrorResources) Apply(
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

func (resources *recordingMirrorResources) Restore(
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

func (resources *recordingMirrorResources) state() ([]string, servicebinding.ServiceInterceptSnapshot) {
	resources.mu.Lock()
	defer resources.mu.Unlock()
	return append([]string(nil), resources.events...), resources.snapshot
}

func (resources *recordingMirrorResources) failRestore(err error) {
	resources.mu.Lock()
	defer resources.mu.Unlock()
	resources.restoreErr = err
}

func startMirrorPrimaryEchoes(t *testing.T) (net.Listener, *net.UDPConn, int32, int32) {
	t.Helper()
	tcpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			connection, acceptErr := tcpListener.Accept()
			if acceptErr != nil {
				return
			}
			go func(connection net.Conn) {
				defer connection.Close()
				buffer := make([]byte, 64)
				count, readErr := connection.Read(buffer)
				if readErr != nil {
					return
				}
				_, _ = connection.Write(append([]byte("primary-tcp:"), buffer[:count]...))
				_, _ = io.Copy(io.Discard, connection)
				closeWrite(connection)
			}(connection)
		}
	}()
	udpListener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		_ = tcpListener.Close()
		t.Fatal(err)
	}
	go func() {
		buffer := make([]byte, 65507)
		for {
			count, remote, readErr := udpListener.ReadFromUDP(buffer)
			if readErr != nil {
				return
			}
			_, _ = udpListener.WriteToUDP(append([]byte("primary-udp:"), buffer[:count]...), remote)
		}
	}()
	return tcpListener, udpListener,
		int32(tcpListener.Addr().(*net.TCPAddr).Port), int32(udpListener.LocalAddr().(*net.UDPAddr).Port)
}

func TestMirrorFullShadowQueueDoesNotDelayPrimary(t *testing.T) {
	clusterClient, clusterGateway := net.Pipe()
	primaryGateway, primaryBackend := net.Pipe()
	defer clusterClient.Close()
	defer primaryBackend.Close()
	var dialed bool
	pool, err := newPrimaryPool([]servicebinding.BackendSet{{
		ServicePort: 80, Protocol: corev1.ProtocolTCP,
		Targets: []servicebinding.BackendTarget{{Address: "127.0.0.1", Port: 8080}},
	}}, func(context.Context, string, string) (net.Conn, error) {
		if dialed {
			return nil, errors.New("primary was dialed more than once")
		}
		dialed = true
		return primaryGateway, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	config := Config{
		PrimaryDialTimeout: time.Second, ShadowQueueSize: 1, Now: time.Now,
	}
	relay := newMirrorRelay(nil, nil, pool, config)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		relay.serveTCP(ctx, Port{ServicePort: 80, Protocol: "tcp"}, clusterGateway)
	}()
	backendDone := make(chan error, 1)
	go func() {
		request := make([]byte, len("request"))
		if _, readErr := io.ReadFull(primaryBackend, request); readErr != nil {
			backendDone <- readErr
			return
		}
		_, writeErr := primaryBackend.Write([]byte("primary-response"))
		backendDone <- writeErr
	}()
	_ = clusterClient.SetDeadline(time.Now().Add(time.Second))
	if _, err := clusterClient.Write([]byte("request")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, len("primary-response"))
	if _, err := io.ReadFull(clusterClient, response); err != nil || string(response) != "primary-response" {
		t.Fatalf("primary response=%q err=%v", response, err)
	}
	if err := <-backendDone; err != nil {
		t.Fatal(err)
	}
	if len(relay.shadow) != 1 {
		t.Fatalf("bounded shadow queue length=%d want=1", len(relay.shadow))
	}
	cancel()
	_ = clusterClient.Close()
	_ = primaryBackend.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("primary relay remained blocked by an unread shadow queue")
	}
}

func TestMirrorDroppedShadowStillOffersTerminalClose(t *testing.T) {
	config := Config{GatewayIP: "127.0.0.1", ShadowQueueSize: 1}
	if err := config.normalize(); err != nil {
		t.Fatal(err)
	}
	relay := newMirrorRelay(nil, nil, nil, config)
	if !relay.emit(mirrorstream.Frame{Type: mirrorstream.Open, StreamID: 7, ServicePort: 80}) {
		t.Fatal("initial shadow frame was not queued")
	}
	if relay.emit(mirrorstream.Frame{Type: mirrorstream.Data, StreamID: 7, Payload: []byte("overflow")}) {
		t.Fatal("overflowing shadow frame was unexpectedly queued")
	}
	<-relay.shadow
	if !relay.emit(mirrorstream.Frame{Type: mirrorstream.Close, StreamID: 7}) {
		t.Fatal("terminal close was not offered after shadow overflow")
	}
	if frame := <-relay.shadow; frame.Type != mirrorstream.Close || frame.StreamID != 7 {
		t.Fatalf("terminal shadow frame=%#v", frame)
	}
}

func TestMirrorStreamRelaysTCPAndUDPThenRestores(t *testing.T) {
	stateStore, principal, active := mirrorStreamStore(t)
	tcpPrimary, udpPrimary, tcpPrimaryPort, udpPrimaryPort := startMirrorPrimaryEchoes(t)
	defer tcpPrimary.Close()
	defer udpPrimary.Close()
	resources := &recordingMirrorResources{
		store: stateStore, tcpPort: tcpPrimaryPort, udpPort: udpPrimaryPort,
	}
	services := &mirrorTestServices{}
	handler, err := New(
		stateStore, mirrorTestSessions{session: active}, services, resources,
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
	task := createMirrorTask(t, handler, principal, active.ID)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if apiError := handler.ServeAPI(writer, request, principal); apiError != nil {
			http.Error(writer, apiError.Message, apiErrorStatus(apiError))
		}
	}))
	t.Cleanup(server.Close)
	streamURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v2/sessions/" + active.ID +
		"/mirrors/" + task.ID + "/stream?namespace=development"
	connection, response, err := websocket.Dial(context.Background(), streamURL, nil)
	if err != nil {
		if response != nil {
			t.Fatalf("dial Mirror stream: %v status=%d", err, response.StatusCode)
		}
		t.Fatal(err)
	}
	defer connection.CloseNow()
	frame := readMirrorFrame(t, connection)
	if frame.Type != mirrorstream.Ready {
		t.Fatalf("first Mirror frame=%#v", frame)
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
	open := readMirrorFrame(t, connection)
	if open.Type != mirrorstream.Open || open.ServicePort != 80 || open.Protocol != mirrorstream.ProtocolTCP {
		t.Fatalf("TCP open frame=%#v", open)
	}
	if _, err := tcpConnection.Write([]byte("cluster-request")); err != nil {
		t.Fatal(err)
	}
	primaryResponse := make([]byte, len("primary-tcp:cluster-request"))
	if _, err := io.ReadFull(tcpConnection, primaryResponse); err != nil || string(primaryResponse) != "primary-tcp:cluster-request" {
		t.Fatalf("TCP primary response=%q err=%v", primaryResponse, err)
	}
	data := readMirrorFrame(t, connection)
	if data.Type != mirrorstream.Data || data.StreamID != open.StreamID || !bytes.Equal(data.Payload, []byte("cluster-request")) {
		t.Fatalf("TCP request frame=%#v", data)
	}
	if err := tcpConnection.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	halfClose := readMirrorFrame(t, connection)
	if halfClose.Type != mirrorstream.CloseWrite || halfClose.StreamID != open.StreamID {
		t.Fatalf("TCP half-close frame=%#v", halfClose)
	}
	_ = tcpConnection.SetReadDeadline(time.Now().Add(time.Second))
	buffer := make([]byte, 1)
	if _, err := tcpConnection.Read(buffer); !errors.Is(err, io.EOF) {
		t.Fatalf("TCP primary half-close did not reach cluster peer: %v", err)
	}

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
	_ = udpConnection.SetReadDeadline(time.Now().Add(time.Second))
	udpResponse := make([]byte, 64)
	count, err := udpConnection.Read(udpResponse)
	if err != nil || string(udpResponse[:count]) != "primary-udp:udp-request" {
		t.Fatalf("UDP primary response=%q err=%v", udpResponse[:count], err)
	}
	var datagram mirrorstream.Frame
	for {
		datagram = readMirrorFrame(t, connection)
		if datagram.Type == mirrorstream.Close {
			continue
		}
		break
	}
	if datagram.Type != mirrorstream.Datagram || datagram.ServicePort != 53 ||
		datagram.Protocol != mirrorstream.ProtocolUDP || string(datagram.Payload) != "udp-request" {
		t.Fatalf("UDP request frame=%#v", datagram)
	}
	writeMirrorFrame(t, connection, mirrorstream.Frame{Type: mirrorstream.Stop})
	for {
		frame, readErr := readMirrorFrameWithError(connection)
		if readErr != nil {
			break
		}
		if frame.Type == mirrorstream.Stop {
			break
		}
	}
	waitForMirrorState(t, stateStore, task.ID, "stopped")
	events, _ := resources.state()
	if strings.Join(events, ",") != "capture,apply,restore" {
		t.Fatalf("resource lifecycle=%v", events)
	}
	snapshots, err := stateStore.ResourceSnapshots().ListByTask(context.Background(), task.ID)
	if err != nil || len(snapshots) != 0 {
		t.Fatalf("completed Mirror snapshots=%#v err=%v", snapshots, err)
	}
}

func TestMirrorStreamRestoresOnStopTokenRevocationAndSessionEnd(t *testing.T) {
	for _, scenario := range []string{"durable-stop", "token-revocation", "session-end"} {
		t.Run(scenario, func(t *testing.T) {
			stateStore, principal, active := mirrorStreamStore(t)
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
			resources := &recordingMirrorResources{store: stateStore}
			handler, err := New(
				stateStore, mirrorTestSessions{session: active}, &mirrorTestServices{}, resources,
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
			task := createMirrorTask(t, handler, principal, active.ID)
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if apiError := handler.ServeAPI(writer, request, principal); apiError != nil {
					http.Error(writer, apiError.Message, apiErrorStatus(apiError))
				}
			}))
			defer server.Close()
			streamURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v2/sessions/" + active.ID +
				"/mirrors/" + task.ID + "/stream?namespace=development"
			connection, _, err := websocket.Dial(context.Background(), streamURL, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer connection.CloseNow()
			if frame := readMirrorFrame(t, connection); frame.Type != mirrorstream.Ready {
				t.Fatalf("ready frame=%#v", frame)
			}
			switch scenario {
			case "durable-stop":
				path := "/api/v2/sessions/" + active.ID + "/mirrors/" + task.ID + "?namespace=development"
				response, apiError := mirrorRequest(handler, principal, http.MethodDelete, path, nil, "")
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
				frame, readErr := readMirrorFrameWithError(connection)
				if readErr != nil || frame.Type == mirrorstream.Stop {
					break
				}
			}
			waitForMirrorState(t, stateStore, task.ID, "stopped")
			events, _ := resources.state()
			if strings.Join(events, ",") != "capture,apply,restore" {
				t.Fatalf("resource lifecycle=%v", events)
			}
		})
	}
}

func TestMirrorStreamRestoreFailureStaysRecoverable(t *testing.T) {
	stateStore, principal, active := mirrorStreamStore(t)
	resources := &recordingMirrorResources{store: stateStore}
	handler, err := New(
		stateStore, mirrorTestSessions{session: active}, &mirrorTestServices{}, resources,
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
	task := createMirrorTask(t, handler, principal, active.ID)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if apiError := handler.ServeAPI(writer, request, principal); apiError != nil {
			http.Error(writer, apiError.Message, apiErrorStatus(apiError))
		}
	}))
	defer server.Close()
	streamURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v2/sessions/" + active.ID +
		"/mirrors/" + task.ID + "/stream?namespace=development"
	connection, _, err := websocket.Dial(context.Background(), streamURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	if frame := readMirrorFrame(t, connection); frame.Type != mirrorstream.Ready {
		t.Fatalf("ready frame=%#v", frame)
	}
	resources.failRestore(errors.New("simulated restore outage"))
	writeMirrorFrame(t, connection, mirrorstream.Frame{Type: mirrorstream.Stop})
	waitForMirrorState(t, stateStore, task.ID, "recovering")
	snapshots, err := stateStore.ResourceSnapshots().ListByTask(context.Background(), task.ID)
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("recoverable Mirror snapshots=%#v err=%v", snapshots, err)
	}
}

func mirrorStreamStore(t *testing.T) (*storage.Store, controller.Principal, sessionapi.ActiveSession) {
	t.Helper()
	ctx := context.Background()
	stateStore, err := storage.Open(ctx, storage.Config{Backend: storage.BackendSQLite, SQLitePath: t.TempDir() + "/mirror.db"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stateStore.Close() })
	now := time.Now().UTC()
	principalID, sessionID := uuid.NewString(), uuid.NewString()
	if _, err := stateStore.Principals().Upsert(ctx, storage.Principal{
		ID: principalID, Provider: "test", ExternalID: "mirror-stream-user", CreatedAt: now, UpdatedAt: now,
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

func createMirrorTask(t *testing.T, handler *Handler, principal controller.Principal, sessionID string) Document {
	t.Helper()
	path := "/api/v2/sessions/" + sessionID + "/mirrors?namespace=development"
	body := []byte(`{"service":"api","ports":[{"servicePort":53,"protocol":"udp"},{"servicePort":80,"protocol":"tcp"}]}`)
	response, apiError := mirrorRequest(handler, principal, http.MethodPost, path, body, uuid.NewString())
	if apiError != nil || response.Code != http.StatusCreated {
		t.Fatalf("create Mirror: status=%d error=%#v body=%s", response.Code, apiError, response.Body.String())
	}
	var document Document
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	return document
}

func readMirrorFrame(t *testing.T, connection *websocket.Conn) mirrorstream.Frame {
	t.Helper()
	frame, err := readMirrorFrameWithError(connection)
	if err != nil {
		t.Fatal(err)
	}
	return frame
}

func readMirrorFrameWithError(connection *websocket.Conn) (mirrorstream.Frame, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	messageType, encoded, err := connection.Read(ctx)
	if err != nil {
		return mirrorstream.Frame{}, err
	}
	if messageType != websocket.MessageBinary {
		return mirrorstream.Frame{}, errors.New("expected binary Mirror frame")
	}
	return mirrorstream.Decode(encoded)
}

func writeMirrorFrame(t *testing.T, connection *websocket.Conn, frame mirrorstream.Frame) {
	t.Helper()
	encoded, err := mirrorstream.Encode(frame)
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

func waitForMirrorState(t *testing.T, stateStore *storage.Store, taskID, state string) {
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
	t.Fatalf("Mirror Task state=%q want=%q err=%v", task.State, state, err)
}

func apiErrorStatus(apiError *controller.APIError) int {
	if apiError.Code == controller.CodeConflict {
		return http.StatusConflict
	}
	return http.StatusBadRequest
}
