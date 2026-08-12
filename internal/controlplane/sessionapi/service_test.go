package sessionapi_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/capability"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
	"github.com/google/uuid"
)

type auditCapture struct {
	mu      sync.Mutex
	records []controlplane.AuditRecord
}

type staticNetworkDiscoverer struct{}

type staticCapabilityDiscoverer struct{}

func (staticCapabilityDiscoverer) DiscoverCapabilities(
	_ context.Context,
	principal controlplaneapi.Principal,
	namespace string,
) (capability.Snapshot, *controlplaneapi.Error) {
	snapshot, err := capability.Normalize(capability.Snapshot{
		SchemaVersion: capability.SchemaVersion, PrincipalID: principal.Subject, Namespace: namespace,
		GatewayVersion: "v2-test", Capabilities: []string{"cluster.tunnel", "pods.list"},
	})
	if err != nil {
		return capability.Snapshot{}, &controlplaneapi.Error{Code: controlplaneapi.CodeInternal, Cause: err}
	}
	return snapshot, nil
}

func (staticNetworkDiscoverer) Discover(
	context.Context,
	controlplaneapi.Principal,
	string,
) (networkspec.Spec, error) {
	return networkspec.Normalize(networkspec.Spec{
		PodCIDRs: []string{"10.2.0.0/16"}, ServiceCIDRs: []string{"10.96.0.0/12"},
		ServiceIPs: []string{"10.96.0.10"}, DNSServer: "10.96.0.10",
	})
}

func (capture *auditCapture) Record(_ context.Context, record controlplane.AuditRecord) error {
	capture.mu.Lock()
	capture.records = append(capture.records, record)
	capture.mu.Unlock()
	return nil
}

func TestClusterSessionLifecycleIsOwnedIdempotentAndAudited(t *testing.T) {
	now := time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC)
	server, stateStore, capture, principalID := newSessionTestServer(t, func() time.Time { return now })
	defer stateStore.Close()

	create := sessionRequest(t, server, http.MethodPost, "/api/v2/sessions?namespace=development", principalID, map[string]string{
		sessionapi.IdempotencyHeader: "desktop-session-1",
	})
	if create.Code != http.StatusCreated || create.Header().Get("ETag") != `"1"` {
		t.Fatalf("create status = %d headers = %#v body = %s", create.Code, create.Header(), create.Body.String())
	}
	document := decodeDocument(t, create)
	if document.ID == "" || document.Namespace != "development" || document.State != "active" || document.Generation != 1 ||
		document.NetworkSpec.Version != networkspec.Version || len(document.NetworkSpec.PodCIDRs) == 0 ||
		len(document.NetworkSpecHash) != 64 || document.Capabilities == nil ||
		document.Capabilities.PrincipalID != principalID || document.Capabilities.Namespace != "development" ||
		len(document.Capabilities.Capabilities) != 2 {
		t.Fatalf("created session = %#v", document)
	}

	replay := sessionRequest(t, server, http.MethodPost, "/api/v2/sessions?namespace=development", principalID, map[string]string{
		sessionapi.IdempotencyHeader: "desktop-session-1",
	})
	replayed := decodeDocument(t, replay)
	if replay.Code != http.StatusOK || replay.Header().Get("Idempotent-Replayed") != "true" || replayed.ID != document.ID {
		t.Fatalf("replay status = %d headers = %#v session = %#v", replay.Code, replay.Header(), replayed)
	}

	mismatch := sessionRequest(t, server, http.MethodPost, "/api/v2/sessions?namespace=production", principalID, map[string]string{
		sessionapi.IdempotencyHeader: "desktop-session-1",
	})
	if mismatch.Code != http.StatusConflict {
		t.Fatalf("idempotency mismatch status = %d body = %s", mismatch.Code, mismatch.Body.String())
	}

	now = now.Add(30 * time.Second)
	heartbeat := sessionRequest(t, server, http.MethodPost,
		"/api/v2/sessions/"+document.ID+"/heartbeat?namespace=development", principalID,
		map[string]string{"If-Match": `"1"`},
	)
	heartbeatDocument := decodeDocument(t, heartbeat)
	if heartbeat.Code != http.StatusOK || heartbeatDocument.Generation != 2 || !heartbeatDocument.ExpiresAt.Equal(now.Add(2*time.Minute)) {
		t.Fatalf("heartbeat status = %d session = %#v", heartbeat.Code, heartbeatDocument)
	}

	stale := sessionRequest(t, server, http.MethodPost,
		"/api/v2/sessions/"+document.ID+"/heartbeat?namespace=development", principalID,
		map[string]string{"If-Match": `"1"`},
	)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale heartbeat status = %d body = %s", stale.Code, stale.Body.String())
	}

	otherPrincipal := uuid.NewString()
	foreignRead := sessionRequest(t, server, http.MethodGet,
		"/api/v2/sessions/"+document.ID+"?namespace=development", otherPrincipal, nil,
	)
	if foreignRead.Code != http.StatusNotFound {
		t.Fatalf("foreign read status = %d body = %s", foreignRead.Code, foreignRead.Body.String())
	}

	taskStates := map[string]remotetask.State{
		"port-forward":  remotetask.Running,
		"exchange":      remotetask.Running,
		"pod-exec":      remotetask.Pending,
		"file-transfer": remotetask.Running,
	}
	taskIDs := make(map[string]string, len(taskStates))
	for taskType, state := range taskStates {
		taskID := uuid.NewString()
		taskIDs[taskType] = taskID
		expiresAt := document.ExpiresAt
		if err := stateStore.Tasks().Create(context.Background(), storage.Task{
			ID: taskID, PrincipalID: principalID, SessionID: document.ID, Type: taskType,
			State: state, Spec: json.RawMessage(`{}`), Result: json.RawMessage(`{}`),
			IdempotencyKey: "task-" + taskType, CreatedAt: now, UpdatedAt: now, ExpiresAt: &expiresAt,
		}); err != nil {
			t.Fatal(err)
		}
	}

	disconnect := sessionRequest(t, server, http.MethodDelete,
		"/api/v2/sessions/"+document.ID+"?namespace=development", principalID,
		map[string]string{"If-Match": `"2"`},
	)
	disconnected := decodeDocument(t, disconnect)
	if disconnect.Code != http.StatusOK || disconnected.State != "disconnected" || disconnected.Generation != 3 {
		t.Fatalf("disconnect status = %d session = %#v", disconnect.Code, disconnected)
	}
	wantTaskStates := map[string]remotetask.State{
		"port-forward":  remotetask.Stopped,
		"exchange":      remotetask.Recovering,
		"pod-exec":      remotetask.Stopped,
		"file-transfer": remotetask.Failed,
	}
	for taskType, want := range wantTaskStates {
		task, err := stateStore.Tasks().GetByID(context.Background(), taskIDs[taskType])
		if err != nil || task.State != want {
			t.Fatalf("%s Task = %#v, %v; want %s", taskType, task, err, want)
		}
	}

	idempotentDisconnect := sessionRequest(t, server, http.MethodDelete,
		"/api/v2/sessions/"+document.ID+"?namespace=development", principalID,
		map[string]string{"If-Match": `"1"`},
	)
	if got := decodeDocument(t, idempotentDisconnect); idempotentDisconnect.Code != http.StatusOK || got.Generation != 3 {
		t.Fatalf("idempotent disconnect status = %d session = %#v", idempotentDisconnect.Code, got)
	}

	capture.mu.Lock()
	defer capture.mu.Unlock()
	foundHeartbeatAudit := false
	for _, record := range capture.records {
		if record.Operation == "heartbeat" && record.SessionID == document.ID && record.Namespace == "development" {
			foundHeartbeatAudit = true
		}
	}
	if !foundHeartbeatAudit {
		t.Fatalf("heartbeat audit missing trusted session ID: %#v", capture.records)
	}
}

func TestExpiredSessionCannotBeResurrected(t *testing.T) {
	now := time.Date(2026, 8, 10, 2, 0, 0, 0, time.UTC)
	server, stateStore, _, principalID := newSessionTestServer(t, func() time.Time { return now })
	defer stateStore.Close()
	create := sessionRequest(t, server, http.MethodPost, "/api/v2/sessions?namespace=development", principalID, map[string]string{
		sessionapi.IdempotencyHeader: "expiring-session",
	})
	document := decodeDocument(t, create)
	now = now.Add(3 * time.Minute)
	heartbeat := sessionRequest(t, server, http.MethodPost,
		"/api/v2/sessions/"+document.ID+"/heartbeat?namespace=development", principalID,
		map[string]string{"If-Match": `"1"`},
	)
	if heartbeat.Code != http.StatusConflict {
		t.Fatalf("expired heartbeat status = %d body = %s", heartbeat.Code, heartbeat.Body.String())
	}
	get := sessionRequest(t, server, http.MethodGet,
		"/api/v2/sessions/"+document.ID+"?namespace=development", principalID, nil,
	)
	if loaded := decodeDocument(t, get); loaded.State != "expired" || loaded.Generation != 2 {
		t.Fatalf("expired session = %#v", loaded)
	}
}

func TestSessionInputValidationStopsBeforeStorageLookup(t *testing.T) {
	now := time.Now()
	server, stateStore, _, principalID := newSessionTestServer(t, func() time.Time { return now })
	defer stateStore.Close()
	tests := []struct {
		method  string
		path    string
		headers map[string]string
	}{
		{method: http.MethodPost, path: "/api/v2/sessions?namespace=Bad_Name", headers: map[string]string{sessionapi.IdempotencyHeader: "key"}},
		{method: http.MethodPost, path: "/api/v2/sessions?namespace=development"},
		{method: http.MethodPost, path: "/api/v2/sessions?namespace=development", headers: map[string]string{sessionapi.IdempotencyHeader: "bad key"}},
		{method: http.MethodPost, path: "/api/v2/sessions/not-a-uuid/heartbeat?namespace=development", headers: map[string]string{"If-Match": `"1"`}},
	}
	for _, test := range tests {
		response := sessionRequest(t, server, test.method, test.path, principalID, test.headers)
		if response.Code != http.StatusBadRequest && response.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d body = %s", test.path, response.Code, response.Body.String())
		}
	}
}

func newSessionTestServer(
	t *testing.T,
	now func() time.Time,
) (*controlplane.Server, *storage.Store, *auditCapture, string) {
	t.Helper()
	stateStore, err := storage.Open(context.Background(), storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "sessions.db"), ControlPlaneReplicas: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	principalID := uuid.NewString()
	createdAt := now().UTC()
	if _, err := stateStore.Principals().Upsert(context.Background(), storage.Principal{
		ID: principalID, Provider: "test", ExternalID: "user-1", CreatedAt: createdAt, UpdatedAt: createdAt,
	}); err != nil {
		stateStore.Close()
		t.Fatal(err)
	}
	handler, err := sessionapi.New(stateStore, sessionapi.Config{
		ClusterID: "test-cluster", SessionTTL: 2 * time.Minute, MaxLifetime: time.Hour, Now: now,
		Networks: staticNetworkDiscoverer{}, Capabilities: staticCapabilityDiscoverer{},
	})
	if err != nil {
		stateStore.Close()
		t.Fatal(err)
	}
	policy, err := authorization.New(authorization.Policy{Rules: []authorization.Rule{{
		ID: "sessions", Subjects: []string{"*"}, Namespaces: []string{"*"},
		Operations: []string{"create", "get", "heartbeat", "delete"}, ResourceKinds: []string{"sessions"},
	}}})
	if err != nil {
		stateStore.Close()
		t.Fatal(err)
	}
	capture := &auditCapture{}
	server, err := controlplane.NewServer(
		controlplane.Config{PublicURL: "https://gateway.example.test"}, controlplane.BuildInfo{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		controlplane.WithAuthenticator(controlplaneapi.AuthenticatorFunc(func(request *http.Request) (controlplaneapi.Principal, *controlplaneapi.Error) {
			subject := request.Header.Get("X-Test-Principal")
			if subject == "" {
				subject = principalID
			}
			return controlplaneapi.Principal{Subject: subject, DeviceID: "device-1"}, nil
		})),
		controlplane.WithAuthorizer(policy), controlplane.WithAuditSink(capture), controlplane.WithAPIRoutes(controlplane.APIRoutes{Sessions: sessionapi.NewRoutes(handler).Endpoints()}),
	)
	if err != nil {
		stateStore.Close()
		t.Fatal(err)
	}
	return server, stateStore, capture, principalID
}

func sessionRequest(
	t *testing.T,
	server *controlplane.Server,
	method, path, principalID string,
	headers map[string]string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, nil)
	request.Header.Set("X-Test-Principal", principalID)
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}

func decodeDocument(t *testing.T, response *httptest.ResponseRecorder) sessionapi.Document {
	t.Helper()
	var document sessionapi.Document
	if err := json.NewDecoder(response.Body).Decode(&document); err != nil {
		t.Fatalf("decode session status %d: %v body=%s", response.Code, err, response.Body.String())
	}
	return document
}
