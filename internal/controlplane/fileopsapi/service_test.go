package fileopsapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"github.com/fengqi-dev/kube-loop/internal/controlplane/fileopsapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/google/uuid"
)

type testSessions struct {
	principalID string
	session     sessionapi.ActiveSession
}

func serveAPI(
	t *testing.T,
	handler *fileopsapi.Service,
	response *httptest.ResponseRecorder,
	request *http.Request,
	principal controlplaneapi.Principal,
) *controlplaneapi.Error {
	t.Helper()
	policy, err := authorization.New(authorization.Policy{Rules: []authorization.Rule{{
		ID: "file-operations-test", Subjects: []string{"*"}, Namespaces: []string{"*"},
		Operations: []string{"list", "create", "update", "delete", "get"}, ResourceKinds: []string{"pod-files"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	server, err := controlplane.NewServer(
		controlplane.Config{PublicURL: "https://gateway.example.test"}, controlplane.BuildInfo{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		controlplane.WithAuthenticator(controlplaneapi.AuthenticatorFunc(func(*http.Request) (controlplaneapi.Principal, *controlplaneapi.Error) {
			return principal, nil
		})),
		controlplane.WithAuthorizer(policy), controlplane.WithAPIRoutes(controlplane.APIRoutes{FileOperations: fileopsapi.NewRoutes(handler).Endpoints()}),
	)
	if err != nil {
		t.Fatal(err)
	}
	server.Handler().ServeHTTP(response, request)
	if response.Code < http.StatusBadRequest {
		return nil
	}
	code := controlplaneapi.CodeInternal
	if response.Code == http.StatusBadRequest {
		code = controlplaneapi.CodeInvalidArgument
	}
	return &controlplaneapi.Error{Code: code, Message: response.Body.String()}
}

func (sessions testSessions) RequireActive(_ context.Context, principal controlplaneapi.Principal, namespace, sessionID string) (sessionapi.ActiveSession, *controlplaneapi.Error) {
	if principal.Subject != sessions.principalID || namespace != sessions.session.Namespace || sessionID != sessions.session.ID {
		return sessionapi.ActiveSession{}, &controlplaneapi.Error{Code: controlplaneapi.CodeNotFound, Message: "resource not found"}
	}
	return sessions.session, nil
}

type testTargets struct {
	mu    sync.Mutex
	calls int
}

func (targets *testTargets) ResolveContainer(_ context.Context, _ controlplaneapi.Principal, namespace, pod, container string) (string, error) {
	targets.mu.Lock()
	targets.calls++
	targets.mu.Unlock()
	if namespace != "development" || pod != "api-0" {
		return "", errors.New("target not found")
	}
	if container == "" {
		return "api", nil
	}
	return container, nil
}

type testOperator struct {
	mu          sync.Mutex
	listCalls   int
	mutateCalls int
	fail        bool
}

func (operator *testOperator) List(_ context.Context, _ controlplaneapi.Principal, _ string, spec fileopsapi.Spec) ([]fileopsapi.Entry, error) {
	operator.mu.Lock()
	operator.listCalls++
	operator.mu.Unlock()
	return []fileopsapi.Entry{{Name: "logs", Path: spec.Path + "/logs", Kind: fileopsapi.KindDirectory}}, nil
}

func (operator *testOperator) Mutate(context.Context, controlplaneapi.Principal, string, fileopsapi.Spec) error {
	operator.mu.Lock()
	defer operator.mu.Unlock()
	operator.mutateCalls++
	if operator.fail {
		return errors.New("sensitive Pod failure")
	}
	return nil
}

func TestListAndMutationTaskAreValidatedOwnedAndIdempotent(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	stateStore, principalID, sessionID, expiresAt := createStore(t, now)
	defer stateStore.Close()
	targets, operator := &testTargets{}, &testOperator{}
	handler, err := fileopsapi.New(stateStore, testSessions{principalID: principalID, session: sessionapi.ActiveSession{
		ID: sessionID, Namespace: "development", ExpiresAt: expiresAt,
	}}, targets, operator, fileopsapi.Config{Now: func() time.Time { return now }, AllowedPathRoots: []string{"/workspace"}})
	if err != nil {
		t.Fatal(err)
	}

	listRequest := request(t, http.MethodPost, sessionID, "list", `{"pod":"api-0","path":"/workspace"}`, "")
	listResponse := httptest.NewRecorder()
	if apiError := serveAPI(t, handler, listResponse, listRequest, controlplaneapi.Principal{Subject: principalID}); apiError != nil {
		t.Fatal(apiError)
	}
	var listing fileopsapi.ListDocument
	if err := json.NewDecoder(listResponse.Body).Decode(&listing); err != nil {
		t.Fatal(err)
	}
	if listResponse.Code != http.StatusOK || listing.Container != "api" || len(listing.Items) != 1 {
		t.Fatalf("listing = %#v status = %d", listing, listResponse.Code)
	}

	createBody := `{"pod":"api-0","path":"/workspace/new","kind":"directory"}`
	created := mutate(t, handler, principalID, sessionID, "create", createBody, "operation-1", http.StatusCreated)
	if created.State != "stopped" || !created.Result.Completed || created.Container != "api" {
		t.Fatalf("created task = %#v", created)
	}
	replayed := mutate(t, handler, principalID, sessionID, "create", createBody, "operation-1", http.StatusOK)
	if replayed.ID != created.ID {
		t.Fatalf("replayed task ID = %q, want %q", replayed.ID, created.ID)
	}
	operator.mu.Lock()
	if operator.mutateCalls != 1 {
		t.Fatalf("mutation calls = %d", operator.mutateCalls)
	}
	operator.mu.Unlock()
	targets.mu.Lock()
	if targets.calls != 2 {
		t.Fatalf("target calls = %d, want list + first mutation", targets.calls)
	}
	targets.mu.Unlock()

	getRequest := request(t, http.MethodGet, sessionID, "operations/"+created.ID, "", "")
	getResponse := httptest.NewRecorder()
	if apiError := serveAPI(t, handler, getResponse, getRequest, controlplaneapi.Principal{Subject: principalID}); apiError != nil {
		t.Fatal(apiError)
	}
	if getResponse.Code != http.StatusOK {
		t.Fatalf("get status = %d", getResponse.Code)
	}
}

func TestMutationFailureIsPersistedWithoutLeakingExecutorDetail(t *testing.T) {
	now := time.Now().UTC()
	stateStore, principalID, sessionID, expiresAt := createStore(t, now)
	defer stateStore.Close()
	operator := &testOperator{fail: true}
	handler, err := fileopsapi.New(stateStore, testSessions{principalID: principalID, session: sessionapi.ActiveSession{
		ID: sessionID, Namespace: "development", ExpiresAt: expiresAt,
	}}, &testTargets{}, operator, fileopsapi.Config{AllowedPathRoots: []string{"/workspace"}})
	if err != nil {
		t.Fatal(err)
	}
	document := mutate(t, handler, principalID, sessionID, "delete", `{"pod":"api-0","path":"/workspace/cache","recursive":true}`, "failure-1", http.StatusCreated)
	if document.State != "failed" || document.Result.Error != "remote file operation failed" || document.Result.Completed {
		t.Fatalf("failed task = %#v", document)
	}
	encoded, _ := json.Marshal(document)
	if bytes.Contains(encoded, []byte("sensitive")) {
		t.Fatalf("executor detail leaked: %s", encoded)
	}
}

func TestInvalidAndRootMutationNeverReachKubernetes(t *testing.T) {
	now := time.Now().UTC()
	stateStore, principalID, sessionID, expiresAt := createStore(t, now)
	defer stateStore.Close()
	targets := &testTargets{}
	handler, err := fileopsapi.New(stateStore, testSessions{principalID: principalID, session: sessionapi.ActiveSession{
		ID: sessionID, Namespace: "development", ExpiresAt: expiresAt,
	}}, targets, &testOperator{}, fileopsapi.Config{AllowedPathRoots: []string{"/workspace"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ action, body string }{
		{"create", `{"pod":"api-0","path":"/workspace","kind":"directory"}`},
		{"rename", `{"pod":"api-0","path":"/workspace/a","destination":"/etc/a"}`},
		{"delete", `{"pod":"api-0","path":"/workspace/../etc","recursive":true}`},
	} {
		response := httptest.NewRecorder()
		apiError := serveAPI(t, handler, response, request(t, http.MethodPost, sessionID, test.action, test.body, uuid.NewString()), controlplaneapi.Principal{Subject: principalID})
		if apiError == nil || apiError.Code != controlplaneapi.CodeInvalidArgument {
			t.Fatalf("%s error = %#v", test.action, apiError)
		}
	}
	targets.mu.Lock()
	defer targets.mu.Unlock()
	if targets.calls != 0 {
		t.Fatalf("invalid requests reached Kubernetes %d times", targets.calls)
	}
}

func mutate(t *testing.T, handler *fileopsapi.Service, principalID, sessionID, action, body, key string, status int) fileopsapi.Document {
	t.Helper()
	response := httptest.NewRecorder()
	apiError := serveAPI(t, handler, response, request(t, http.MethodPost, sessionID, action, body, key), controlplaneapi.Principal{Subject: principalID})
	if apiError != nil {
		t.Fatal(apiError)
	}
	if response.Code != status {
		t.Fatalf("status = %d, want %d: %s", response.Code, status, response.Body.String())
	}
	var document fileopsapi.Document
	if err := json.NewDecoder(response.Body).Decode(&document); err != nil {
		t.Fatal(err)
	}
	return document
}

func request(t *testing.T, method, sessionID, suffix, body, key string) *http.Request {
	t.Helper()
	var content *bytes.Reader
	if body != "" {
		content = bytes.NewReader([]byte(body))
	} else {
		content = bytes.NewReader(nil)
	}
	request := httptest.NewRequest(method, "/kubeloop/api/sessions/"+sessionID+"/pod-files/"+suffix+"?namespace=development", content)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	return request
}

func createStore(t *testing.T, now time.Time) (*storage.Store, string, string, time.Time) {
	t.Helper()
	stateStore, err := storage.Open(context.Background(), storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "file-operations.db"), ControlPlaneReplicas: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	principalID, sessionID := uuid.NewString(), uuid.NewString()
	if _, err := stateStore.Principals().Upsert(context.Background(), storage.Principal{
		ID: principalID, Provider: "test", ExternalID: "file-operations-user", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	network, _ := networkspec.Normalize(networkspec.Spec{PodCIDRs: []string{"10.244.0.0/16"}})
	networkJSON, _ := networkspec.CanonicalJSON(network)
	networkHash, _ := networkspec.Hash(network)
	expiresAt := now.Add(time.Hour)
	if err := stateStore.Sessions().Create(context.Background(), storage.Session{
		ID: sessionID, PrincipalID: principalID, DeviceID: "device", ClusterID: "cluster", Namespace: "development",
		State: "active", Generation: 1, NetworkSpec: networkJSON, NetworkSpecHash: networkHash,
		CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: expiresAt,
	}); err != nil {
		t.Fatal(err)
	}
	return stateStore, principalID, sessionID, expiresAt
}
