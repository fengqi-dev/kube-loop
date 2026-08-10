package execapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/fengqi-dev/kube-loop/internal/controller"
	"github.com/fengqi-dev/kube-loop/internal/controller/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controller/execapi"
	"github.com/fengqi-dev/kube-loop/internal/controller/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controller/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/execstream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/google/uuid"
)

type sessionValidator struct {
	principal string
	session   sessionapi.ActiveSession
}

func (validator sessionValidator) RequireActive(
	_ context.Context,
	principal controller.Principal,
	namespace, id string,
) (sessionapi.ActiveSession, *controller.APIError) {
	if principal.Subject != validator.principal || namespace != validator.session.Namespace || id != validator.session.ID {
		return sessionapi.ActiveSession{}, &controller.APIError{Code: controller.CodeNotFound, Message: "resource not found"}
	}
	return validator.session, nil
}

type fakeExecutor struct {
	mu        sync.Mutex
	validated int
	executed  int
}

type blockingExecutor struct{ started chan struct{} }

func (executor *blockingExecutor) Validate(context.Context, controller.Principal, string, execapi.Spec) error {
	return nil
}

func (executor *blockingExecutor) Exec(
	ctx context.Context,
	_ controller.Principal,
	_ string,
	_ execapi.Spec,
	_ execapi.Streams,
) error {
	close(executor.started)
	<-ctx.Done()
	return ctx.Err()
}

func (executor *fakeExecutor) Validate(context.Context, controller.Principal, string, execapi.Spec) error {
	executor.mu.Lock()
	executor.validated++
	executor.mu.Unlock()
	return nil
}

func (executor *fakeExecutor) Exec(
	_ context.Context,
	_ controller.Principal,
	_ string,
	_ execapi.Spec,
	streams execapi.Streams,
) error {
	executor.mu.Lock()
	executor.executed++
	executor.mu.Unlock()
	_, _ = streams.Stdout.Write([]byte("hello\n"))
	_, _ = streams.Stderr.Write([]byte("warning\n"))
	return nil
}

func TestPodExecTaskAndWebSocketStreamAreOwnedAndSingleUse(t *testing.T) {
	now := time.Now().UTC()
	stateStore, err := storage.Open(context.Background(), storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "exec.db"), ControllerReplicas: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stateStore.Close()
	principalID := uuid.NewString()
	sessionID := uuid.NewString()
	if _, err := stateStore.Principals().Upsert(context.Background(), storage.Principal{
		ID: principalID, Provider: "test", ExternalID: "user", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	network, _ := networkspec.Normalize(networkspec.Spec{PodCIDRs: []string{"10.244.0.0/16"}, ServiceIPs: []string{"10.96.0.10"}, DNSServer: "10.96.0.10"})
	networkJSON, _ := networkspec.CanonicalJSON(network)
	networkHash, _ := networkspec.Hash(network)
	expiresAt := now.Add(time.Hour)
	if err := stateStore.Sessions().Create(context.Background(), storage.Session{
		ID: sessionID, PrincipalID: principalID, DeviceID: "device", ClusterID: "cluster",
		Namespace: "development", State: "active", Generation: 1,
		NetworkSpec: networkJSON, NetworkSpecHash: networkHash,
		CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: expiresAt,
	}); err != nil {
		t.Fatal(err)
	}
	executor := &fakeExecutor{}
	handler, err := execapi.New(stateStore, sessionValidator{principal: principalID, session: sessionapi.ActiveSession{
		ID: sessionID, Namespace: "development", ExpiresAt: expiresAt, NetworkSpecHash: networkHash,
	}}, executor, execapi.Config{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	router := controller.NewAPIRouter()
	if err := router.Handle(http.MethodPost, "/api/v2/sessions/{sessionID}/exec", handler); err != nil {
		t.Fatal(err)
	}
	if err := router.Handle(http.MethodGet, "/api/v2/sessions/{sessionID}/exec/{taskID}/stream", handler); err != nil {
		t.Fatal(err)
	}
	policy, _ := authorization.New(authorization.Policy{Rules: []authorization.Rule{{
		ID: "exec", Subjects: []string{"*"}, Namespaces: []string{"development"},
		Operations: []string{"create", "stream"}, ResourceKinds: []string{"pod-exec"},
	}}})
	server, err := controller.NewServer(controller.Config{PublicURL: "https://gateway.example.test"}, controller.BuildInfo{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		controller.WithAuthenticator(controller.AuthenticatorFunc(func(request *http.Request) (controller.Principal, *controller.APIError) {
			return controller.Principal{Subject: request.Header.Get("X-Principal"), DeviceID: "device"}, nil
		})), controller.WithAuthorizer(policy), controller.WithAPIHandler(router))
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	createRequest, _ := http.NewRequest(http.MethodPost, httpServer.URL+"/api/v2/sessions/"+sessionID+"/exec?namespace=development",
		bytes.NewBufferString(`{"pod":"api-0","container":"api","command":["/bin/sh"],"tty":false}`))
	createRequest.Header.Set("X-Principal", principalID)
	createRequest.Header.Set("Content-Type", "application/json")
	createRequest.Header.Set("Idempotency-Key", "exec-1")
	createResponse, err := http.DefaultClient.Do(createRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer createResponse.Body.Close()
	var document execapi.Document
	if createResponse.StatusCode != http.StatusCreated || json.NewDecoder(createResponse.Body).Decode(&document) != nil || document.State != "pending" {
		t.Fatalf("create status = %d task = %#v", createResponse.StatusCode, document)
	}
	streamURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/api/v2/sessions/" + sessionID + "/exec/" + document.ID + "/stream?namespace=development"
	connection, response, err := websocket.Dial(context.Background(), streamURL, &websocket.DialOptions{HTTPHeader: http.Header{"X-Principal": {principalID}}})
	if err != nil {
		if response != nil {
			t.Fatalf("dial status = %d err = %v", response.StatusCode, err)
		}
		t.Fatal(err)
	}
	defer connection.CloseNow()
	frames := make(map[byte]execstream.Frame)
	for len(frames) < 3 {
		messageType, encoded, err := connection.Read(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if messageType != websocket.MessageBinary {
			t.Fatalf("message type = %v", messageType)
		}
		frame, err := execstream.Decode(encoded)
		if err != nil {
			t.Fatal(err)
		}
		frames[frame.Type] = frame
	}
	if string(frames[execstream.Stdout].Payload) != "hello\n" || string(frames[execstream.Stderr].Payload) != "warning\n" {
		t.Fatalf("frames = %#v", frames)
	}
	status, err := execstream.DecodeExit(frames[execstream.Exit])
	if err != nil || status.Code != 0 {
		t.Fatalf("exit = %#v err = %v", status, err)
	}
	task, err := stateStore.Tasks().GetByID(context.Background(), document.ID)
	if err != nil || task.State != "stopped" {
		t.Fatalf("stored task = %#v err = %v", task, err)
	}
	transitionEvents, err := stateStore.Audit().List(context.Background(), storage.AuditFilter{
		Action: storage.TaskTransitionAuditAction, Limit: 10,
	})
	if err != nil || len(transitionEvents) != 3 {
		t.Fatalf("Task transition audit events = %#v, %v", transitionEvents, err)
	}
	streamRequestID := ""
	if response != nil {
		streamRequestID = response.Header.Get(controller.RequestIDHeader)
	}
	apiEvents, backgroundEvents := 0, 0
	for _, event := range transitionEvents {
		if event.PrincipalID != principalID || event.ResourceType != execapi.TaskType || event.ResourceID != document.ID {
			t.Fatalf("Task transition audit event = %#v", event)
		}
		metadata := string(event.Metadata)
		if strings.Contains(metadata, "/bin/sh") || strings.Contains(metadata, "hello") || strings.Contains(metadata, "warning") {
			t.Fatalf("Pod exec command or output leaked into audit metadata: %s", metadata)
		}
		if strings.Contains(metadata, `"source":"api"`) {
			apiEvents++
			if streamRequestID == "" || event.RequestID != streamRequestID {
				t.Fatalf("API transition request ID = %q, WebSocket request ID = %q", event.RequestID, streamRequestID)
			}
		} else if strings.Contains(metadata, `"source":"background"`) {
			backgroundEvents++
		}
	}
	if apiEvents != 2 || backgroundEvents != 1 {
		t.Fatalf("Task transition audit sources: api=%d background=%d", apiEvents, backgroundEvents)
	}
	_, replayResponse, replayErr := websocket.Dial(context.Background(), streamURL, &websocket.DialOptions{HTTPHeader: http.Header{"X-Principal": {principalID}}})
	if replayErr == nil || replayResponse == nil || replayResponse.StatusCode != http.StatusConflict {
		t.Fatalf("replay response = %#v err = %v", replayResponse, replayErr)
	}
	executor.mu.Lock()
	defer executor.mu.Unlock()
	if executor.validated != 1 || executor.executed != 1 {
		t.Fatalf("validate = %d execute = %d", executor.validated, executor.executed)
	}
}

func TestPodExecStreamStopsWhenTokenFamilyIsRevoked(t *testing.T) {
	now := time.Now().UTC()
	stateStore, err := storage.Open(context.Background(), storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "exec-revoke.db"), ControllerReplicas: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stateStore.Close()
	principalID, familyID, sessionID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := stateStore.Principals().Upsert(context.Background(), storage.Principal{
		ID: principalID, Provider: "test", ExternalID: "revoked-user", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.TokenFamilies().Create(context.Background(), storage.TokenFamily{
		ID: familyID, PrincipalID: principalID, DeviceID: "device",
		RefreshTokenHash: bytes.Repeat([]byte{9}, 32), CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	network, _ := networkspec.Normalize(networkspec.Spec{PodCIDRs: []string{"10.244.0.0/16"}, ServiceIPs: []string{"10.96.0.10"}, DNSServer: "10.96.0.10"})
	networkJSON, _ := networkspec.CanonicalJSON(network)
	networkHash, _ := networkspec.Hash(network)
	expiresAt := now.Add(time.Hour)
	if err := stateStore.Sessions().Create(context.Background(), storage.Session{
		ID: sessionID, PrincipalID: principalID, DeviceID: "device", ClusterID: "cluster",
		Namespace: "development", State: "active", Generation: 1,
		NetworkSpec: networkJSON, NetworkSpecHash: networkHash,
		CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: expiresAt,
	}); err != nil {
		t.Fatal(err)
	}
	executor := &blockingExecutor{started: make(chan struct{})}
	handler, err := execapi.New(stateStore, sessionValidator{principal: principalID, session: sessionapi.ActiveSession{
		ID: sessionID, Namespace: "development", ExpiresAt: expiresAt, NetworkSpecHash: networkHash,
	}}, executor, execapi.Config{CredentialCheckInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	router := controller.NewAPIRouter()
	if err := router.Handle(http.MethodPost, "/api/v2/sessions/{sessionID}/exec", handler); err != nil {
		t.Fatal(err)
	}
	if err := router.Handle(http.MethodGet, "/api/v2/sessions/{sessionID}/exec/{taskID}/stream", handler); err != nil {
		t.Fatal(err)
	}
	policy, _ := authorization.New(authorization.Policy{Rules: []authorization.Rule{{
		ID: "exec", Subjects: []string{"*"}, Namespaces: []string{"development"},
		Operations: []string{"create", "stream"}, ResourceKinds: []string{"pod-exec"},
	}}})
	server, err := controller.NewServer(controller.Config{PublicURL: "https://gateway.example.test"}, controller.BuildInfo{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		controller.WithAuthenticator(controller.AuthenticatorFunc(func(*http.Request) (controller.Principal, *controller.APIError) {
			return controller.Principal{
				Subject: principalID, DeviceID: "device", FamilyID: familyID, AccessExpiresAt: expiresAt,
			}, nil
		})), controller.WithAuthorizer(policy), controller.WithAPIHandler(router))
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	createRequest, _ := http.NewRequest(http.MethodPost, httpServer.URL+"/api/v2/sessions/"+sessionID+"/exec?namespace=development",
		bytes.NewBufferString(`{"pod":"api-0","container":"api","command":["/bin/sh"],"tty":true}`))
	createRequest.Header.Set("Content-Type", "application/json")
	createRequest.Header.Set("Idempotency-Key", "exec-revoke")
	createResponse, err := http.DefaultClient.Do(createRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer createResponse.Body.Close()
	var document execapi.Document
	if createResponse.StatusCode != http.StatusCreated || json.NewDecoder(createResponse.Body).Decode(&document) != nil {
		t.Fatalf("create status = %d task = %#v", createResponse.StatusCode, document)
	}
	streamURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/api/v2/sessions/" + sessionID + "/exec/" + document.ID + "/stream?namespace=development"
	connection, response, err := websocket.Dial(context.Background(), streamURL, nil)
	if err != nil {
		if response != nil {
			t.Fatalf("dial status = %d err = %v", response.StatusCode, err)
		}
		t.Fatal(err)
	}
	defer connection.CloseNow()
	select {
	case <-executor.started:
	case <-time.After(time.Second):
		t.Fatal("Kubernetes exec did not start")
	}
	if err := stateStore.TokenFamilies().Revoke(context.Background(), familyID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	readContext, readCancel := context.WithTimeout(context.Background(), time.Second)
	defer readCancel()
	_, encoded, err := connection.Read(readContext)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := execstream.Decode(encoded)
	if err != nil || frame.Type != execstream.Exit {
		t.Fatalf("exit frame = %#v err = %v", frame, err)
	}
	status, err := execstream.DecodeExit(frame)
	if err != nil || !status.Cancelled {
		t.Fatalf("exit status = %#v err = %v", status, err)
	}
	storedTask, err := stateStore.Tasks().GetByID(context.Background(), document.ID)
	if err != nil || storedTask.State != "stopped" {
		t.Fatalf("stored task = %#v err = %v", storedTask, err)
	}
}
