package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authorization"
	adminsession "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/session"
	admintoken "github.com/fengqi-dev/kube-loop/internal/controlplane/authn/token"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/relayregistry"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relaycontrol"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
	"github.com/google/uuid"
)

func TestManagementReadListsUseStableCursorAndRedactPayloads(t *testing.T) {
	handler, store := newPrincipalTokenHandler(t, false)
	cookie := exchangeManagementToken(t, handler)
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Hour)
	provider := "list-provider"
	for index := range 3 {
		if _, err := store.Principals().Upsert(ctx, storage.Principal{
			ID: uuid.NewString(), Provider: provider, ExternalID: "sensitive-external-" + string(rune('a'+index)),
			DisplayName: "List User", Email: "list@example.test", Groups: []string{"auditors"},
			CreatedAt: now.Add(time.Duration(index) * time.Minute), UpdatedAt: now.Add(time.Duration(index) * time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
	}
	first := authenticatedGET(handler, cookie, "/principals?provider=list-provider&limit=2")
	if first.Code != http.StatusOK || strings.Contains(first.Body.String(), "sensitive-external") {
		t.Fatalf("principal first page status=%d body=%s", first.Code, first.Body.String())
	}
	var firstPage struct {
		Items      []principalDocument `json:"items"`
		NextCursor string              `json:"nextCursor"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &firstPage); err != nil {
		t.Fatal(err)
	}
	if len(firstPage.Items) != 2 || firstPage.NextCursor == "" {
		t.Fatalf("principal first page=%+v", firstPage)
	}
	second := authenticatedGET(handler, cookie, "/principals?provider=list-provider&limit=2&cursor="+firstPage.NextCursor)
	var secondPage struct {
		Items      []principalDocument `json:"items"`
		NextCursor string              `json:"nextCursor"`
	}
	if second.Code != http.StatusOK || json.Unmarshal(second.Body.Bytes(), &secondPage) != nil ||
		len(secondPage.Items) != 1 || secondPage.NextCursor != "" || secondPage.Items[0].ID == firstPage.Items[0].ID ||
		secondPage.Items[0].ID == firstPage.Items[1].ID {
		t.Fatalf("principal second page status=%d body=%s", second.Code, second.Body.String())
	}
	invalid := authenticatedGET(handler, cookie, "/principals?cursor=not-a-cursor")
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid cursor status=%d body=%s", invalid.Code, invalid.Body.String())
	}
	for _, path := range []string{
		"/sessions?principalId=not-a-uuid", "/sessions?namespace=Invalid_Namespace",
		"/tasks?sessionId=not-a-uuid", "/tasks?state=not-a-state",
		"/audit?principalId=not-a-uuid", "/audit?after=not-a-time", "/principals?limit=101",
	} {
		response := authenticatedGET(handler, cookie, path)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid path=%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}

	owner, err := store.Principals().Upsert(ctx, storage.Principal{
		ID: uuid.NewString(), Provider: provider, ExternalID: "task-owner", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	session := storage.Session{
		ID: uuid.NewString(), PrincipalID: owner.ID, DeviceID: "device", ClusterID: "cluster",
		Namespace: "payments", State: "active", CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	if err := store.Sessions().Create(ctx, session); err != nil {
		t.Fatal(err)
	}
	task := storage.Task{
		ID: uuid.NewString(), PrincipalID: owner.ID, SessionID: session.ID, Type: "pod-exec",
		State: remotetask.Running, Spec: json.RawMessage(`{"command":["sensitive-command"]}`),
		Result: json.RawMessage(`{"output":"sensitive-output"}`), IdempotencyKey: "read-list-task",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.Tasks().Create(ctx, task); err != nil {
		t.Fatal(err)
	}
	taskList := authenticatedGET(handler, cookie, "/tasks?namespace=payments&type=pod-exec")
	if taskList.Code != http.StatusOK || strings.Contains(taskList.Body.String(), "sensitive-command") ||
		strings.Contains(taskList.Body.String(), "sensitive-output") {
		t.Fatalf("task list status=%d body=%s", taskList.Code, taskList.Body.String())
	}
	action := "sensitive.audit." + uuid.NewString()
	if err := store.Audit().Append(ctx, storage.AuditEvent{
		ID: uuid.NewString(), PrincipalID: owner.ID, Action: action, Outcome: "success", RequestID: uuid.NewString(),
		Metadata: json.RawMessage(`{"secret":"sensitive-metadata"}`), CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	auditList := authenticatedGET(handler, cookie, "/audit?action="+action)
	if auditList.Code != http.StatusOK || strings.Contains(auditList.Body.String(), "sensitive-metadata") {
		t.Fatalf("audit list status=%d body=%s", auditList.Code, auditList.Body.String())
	}
}

func TestNamespaceAdminListsOnlyExplicitNamespaceBeforeObjectLookup(t *testing.T) {
	handler, store, principal := newNamespaceTokenHandler(t, "payments")
	cookie := exchangeManagementToken(t, handler)
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Minute)
	for index, namespace := range []string{"payments", "other"} {
		session := storage.Session{
			ID: uuid.NewString(), PrincipalID: principal.ID, DeviceID: "device", ClusterID: "cluster",
			Namespace: namespace, State: "active", CreatedAt: now.Add(time.Duration(index) * time.Second),
			ExpiresAt: now.Add(time.Hour),
		}
		if err := store.Sessions().Create(ctx, session); err != nil {
			t.Fatal(err)
		}
		if err := store.Tasks().Create(ctx, storage.Task{
			ID: uuid.NewString(), PrincipalID: principal.ID, SessionID: session.ID, Type: "preview",
			State: remotetask.Running, Spec: json.RawMessage(`{}`), IdempotencyKey: "namespace-" + namespace,
			CreatedAt: session.CreatedAt, UpdatedAt: session.CreatedAt,
		}); err != nil {
			t.Fatal(err)
		}
	}
	allowed := authenticatedGET(handler, cookie, "/sessions?namespace=payments")
	if allowed.Code != http.StatusOK || !strings.Contains(allowed.Body.String(), `"namespace":"payments"`) ||
		strings.Contains(allowed.Body.String(), `"namespace":"other"`) {
		t.Fatalf("allowed namespace list status=%d body=%s", allowed.Code, allowed.Body.String())
	}
	for _, path := range []string{"/sessions?namespace=other", "/tasks?namespace=other", "/sessions", "/tasks"} {
		denied := authenticatedGET(handler, cookie, path)
		if denied.Code != http.StatusForbidden {
			t.Fatalf("path=%s status=%d body=%s", path, denied.Code, denied.Body.String())
		}
	}
	capabilities := authenticatedGET(handler, cookie, "/capabilities")
	if capabilities.Code != http.StatusOK || !strings.Contains(capabilities.Body.String(), `"namespace":"payments"`) ||
		!strings.Contains(capabilities.Body.String(), `"admin.session/list"`) ||
		!strings.Contains(capabilities.Body.String(), `"admin.task/list"`) ||
		strings.Contains(capabilities.Body.String(), `"namespace":"other"`) {
		t.Fatalf("namespace capabilities status=%d body=%s", capabilities.Code, capabilities.Body.String())
	}
}

func TestManagementRelayListPaginatesAndRedactsTransportIdentity(t *testing.T) {
	handler, _ := newPrincipalTokenHandler(t, false, WithRelayStatusSource(relayStatusStub{statuses: []relayregistry.RelayStatus{
		{
			RelayID: "relay-c", Endpoint: "wss://secret-c.example/tunnel", State: relaycontrol.StateDraining,
			DesiredState: relaycontrol.StateDraining, Capacity: relaycontrol.Capacity{MaximumLogicalStreams: 30},
			LeaseExpiresAt: time.Now().UTC().Add(time.Minute), LastHeartbeatAt: time.Now().UTC(),
			Topology: map[string]string{"secret-zone": "c"},
		},
		{
			RelayID: "relay-a", Endpoint: "wss://secret-a.example/tunnel", State: relaycontrol.StateReady,
			DesiredState: relaycontrol.StateReady, Capacity: relaycontrol.Capacity{MaximumLogicalStreams: 10},
			LeaseExpiresAt: time.Now().UTC().Add(time.Minute), LastHeartbeatAt: time.Now().UTC(), Online: true,
			Topology: map[string]string{"secret-zone": "a"},
		},
		{
			RelayID: "relay-b", Endpoint: "wss://secret-b.example/tunnel", State: relaycontrol.StateReady,
			DesiredState: relaycontrol.StateReady, Capacity: relaycontrol.Capacity{MaximumLogicalStreams: 20},
			LeaseExpiresAt: time.Now().UTC().Add(time.Minute), LastHeartbeatAt: time.Now().UTC(), Online: true,
			Topology: map[string]string{"secret-zone": "b"},
		},
	}}))
	cookie := exchangeManagementToken(t, handler)
	first := authenticatedGET(handler, cookie, "/relays?state=ready&online=true&limit=1")
	if first.Code != http.StatusOK || strings.Contains(first.Body.String(), "secret-") {
		t.Fatalf("Relay first page status=%d body=%s", first.Code, first.Body.String())
	}
	var firstPage struct {
		Items      []relayDocument `json:"items"`
		NextCursor string          `json:"nextCursor"`
	}
	if json.Unmarshal(first.Body.Bytes(), &firstPage) != nil || len(firstPage.Items) != 1 ||
		firstPage.Items[0].RelayID != "relay-a" || firstPage.NextCursor == "" {
		t.Fatalf("Relay first page=%s", first.Body.String())
	}
	second := authenticatedGET(handler, cookie, "/relays?state=ready&online=true&limit=1&cursor="+firstPage.NextCursor)
	var secondPage struct {
		Items      []relayDocument `json:"items"`
		NextCursor string          `json:"nextCursor"`
	}
	if second.Code != http.StatusOK || json.Unmarshal(second.Body.Bytes(), &secondPage) != nil ||
		len(secondPage.Items) != 1 || secondPage.Items[0].RelayID != "relay-b" || secondPage.NextCursor != "" {
		t.Fatalf("Relay second page status=%d body=%s", second.Code, second.Body.String())
	}
	for _, path := range []string{"/relays?state=invalid", "/relays?online=maybe", "/relays?cursor=not-a-cursor"} {
		response := authenticatedGET(handler, cookie, path)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("path=%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
}

type relayStatusStub struct {
	statuses []relayregistry.RelayStatus
}

func (stub relayStatusStub) Snapshot() []relayregistry.RelayStatus {
	return append([]relayregistry.RelayStatus(nil), stub.statuses...)
}

func exchangeManagementToken(t *testing.T, handler http.Handler) *http.Cookie {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/sessions/token", bytes.NewBufferString(`{}`))
	request.RemoteAddr = "192.0.2.40:5000"
	request.Header.Set("Origin", "https://gateway.example")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer valid-access-token")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated || len(recorder.Result().Cookies()) != 1 {
		t.Fatalf("token exchange status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	return recorder.Result().Cookies()[0]
}

func authenticatedGET(handler http.Handler, cookie *http.Cookie, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func newNamespaceTokenHandler(t *testing.T, namespace string) (*Handler, *storage.Store, storage.Principal) {
	t.Helper()
	store, err := storage.Open(context.Background(), storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "management.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	principal, err := store.Principals().Upsert(context.Background(), storage.Principal{
		ID: uuid.NewString(), Provider: "oidc", ExternalID: uuid.NewString(), CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	family := storage.TokenFamily{
		ID: uuid.NewString(), PrincipalID: principal.ID, DeviceID: "browser", RefreshTokenHash: bytes.Repeat([]byte{24}, 32),
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	if err := store.TokenFamilies().Create(context.Background(), family); err != nil {
		t.Fatal(err)
	}
	sessions, err := adminsession.New(store, &testVerifier{})
	if err != nil {
		t.Fatal(err)
	}
	authorizer, err := adminauthorization.New(adminauthorization.Snapshot{
		Version: adminauthorization.CurrentVersion, Revision: 1, Assignments: []adminauthorization.Assignment{{
			ID: uuid.NewString(), Role: adminauthorization.RoleNamespaceAdmin,
			Subjects: []string{principal.ID}, Namespaces: []string{namespace},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(
		Config{PublicURL: "https://gateway.example"}, sessions,
		WithReadAPI(authorizer, store, BuildInfo{Version: "test", Commit: "test", ProtocolMin: "2.0", ProtocolMax: "2.0"}),
		WithTokenExchange(tokenAuthenticatorStub{identity: admintoken.AccessIdentity{
			Principal: principal, FamilyID: family.ID, DeviceID: family.DeviceID, AccessExpiresAt: now.Add(time.Minute),
		}}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return handler, store, principal
}
