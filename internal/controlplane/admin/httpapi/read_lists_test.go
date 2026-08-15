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
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/relayregistry"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relaycontrol"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
	"github.com/google/uuid"
)

func TestManagementReadListsUseStableCursorAndRedactPayloads(t *testing.T) {
	handler, store := newIdentityTokenHandler(t, false)
	cookie := exchangeManagementToken(t, handler)
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Hour)
	search := "list-provider"
	for index := range 3 {
		if _, err := store.Identities().Create(ctx, storage.Identity{
			ID: uuid.NewString(), Type: "human", Status: "active",
			DisplayName: search + "-" + string(rune('a'+index)), PrimaryEmail: "list@example.test",
			CreatedAt: now.Add(time.Duration(index) * time.Minute), UpdatedAt: now.Add(time.Duration(index) * time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
	}
	first := authenticatedGET(handler, cookie, "/identities?search=list-provider&limit=2")
	if first.Code != http.StatusOK || strings.Contains(first.Body.String(), "sensitive-external") {
		t.Fatalf("identity first page status=%d body=%s", first.Code, first.Body.String())
	}
	var firstPage struct {
		Items      []identityDocument `json:"items"`
		NextCursor string             `json:"nextCursor"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &firstPage); err != nil {
		t.Fatal(err)
	}
	if len(firstPage.Items) != 2 || firstPage.NextCursor == "" {
		t.Fatalf("identity first page=%+v", firstPage)
	}
	second := authenticatedGET(handler, cookie, "/identities?search=list-provider&limit=2&cursor="+firstPage.NextCursor)
	var secondPage struct {
		Items      []identityDocument `json:"items"`
		NextCursor string             `json:"nextCursor"`
	}
	if second.Code != http.StatusOK || json.Unmarshal(second.Body.Bytes(), &secondPage) != nil ||
		len(secondPage.Items) != 1 || secondPage.NextCursor != "" || secondPage.Items[0].ID == firstPage.Items[0].ID ||
		secondPage.Items[0].ID == firstPage.Items[1].ID {
		t.Fatalf("identity second page status=%d body=%s", second.Code, second.Body.String())
	}
	invalid := authenticatedGET(handler, cookie, "/identities?cursor=not-a-cursor")
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid cursor status=%d body=%s", invalid.Code, invalid.Body.String())
	}
	for _, path := range []string{
		"/sessions?identityId=not-a-uuid", "/sessions?namespace=Invalid_Namespace",
		"/tasks?sessionId=not-a-uuid", "/tasks?state=not-a-state",
		"/audit?identityId=not-a-uuid", "/audit?after=not-a-time", "/identities?limit=101",
	} {
		response := authenticatedGET(handler, cookie, path)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid path=%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}

	owner, err := store.Identities().Create(ctx, storage.Identity{
		ID: uuid.NewString(), Type: "human", DisplayName: "Task Owner", Status: "active", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	organizationID := uuid.NewString()
	if err := store.Organizations().Create(ctx, storage.Organization{ID: organizationID, Name: "Payments", Slug: "payments", Status: "active", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	session := storage.Session{
		ID: uuid.NewString(), IdentityID: owner.ID, DeviceID: "device", ClusterID: "cluster",
		Namespace: "payments", State: "active", CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	if err := store.Sessions().Create(ctx, session); err != nil {
		t.Fatal(err)
	}
	task := storage.Task{
		ID: uuid.NewString(), IdentityID: owner.ID, SessionID: session.ID, Type: "pod-exec",
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
		ID: uuid.NewString(), IdentityID: owner.ID, Action: action, Outcome: "success", RequestID: uuid.NewString(),
		Metadata: json.RawMessage(`{"secret":"sensitive-metadata"}`), CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	auditList := authenticatedGET(handler, cookie, "/audit?action="+action)
	if auditList.Code != http.StatusOK || strings.Contains(auditList.Body.String(), "sensitive-metadata") {
		t.Fatalf("audit list status=%d body=%s", auditList.Code, auditList.Body.String())
	}
}

func TestOAuthGrantListPaginatesFiltersAndRedactsRequestMaterial(t *testing.T) {
	handler, store := newIdentityTokenHandler(t, false)
	cookie := exchangeManagementToken(t, handler)
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Hour)
	identity, err := store.Identities().Create(ctx, storage.Identity{
		ID: uuid.NewString(), Type: "human", DisplayName: "Grant Owner", Status: "active", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.OAuthClients().Create(ctx, storage.OAuthClient{
		ID: "grant-list-client", Name: "Grant list client", Public: true,
		RedirectURIs: []string{"http://127.0.0.1/callback"}, GrantTypes: []string{"authorization_code"},
		Scopes: []string{"openid", "kubeloop.api"}, Enabled: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	for index := range 3 {
		requestID := uuid.NewString()
		if err := store.OAuthSessions().Create(ctx, storage.OAuthSession{
			Kind: "refresh_token", SignatureHash: bytes.Repeat([]byte{byte(index + 31)}, 32), RequestID: requestID,
			IdentityID: identity.ID, ClientID: "grant-list-client", DeviceID: "device-secret",
			RequestJSON: json.RawMessage(`{"granted_scopes":["openid","kubeloop.api"],"secret":"request-secret"}`),
			Status:      "active", CreatedAt: now.Add(time.Duration(index) * time.Minute), ExpiresAt: now.Add(3 * time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}
	first := authenticatedGET(handler, cookie, "/oauth-grants?clientId=grant-list-client&status=active&limit=2")
	if first.Code != http.StatusOK || strings.Contains(first.Body.String(), "request-secret") ||
		strings.Contains(first.Body.String(), "signature") {
		t.Fatalf("status=%d body=%s", first.Code, first.Body.String())
	}
	var page struct {
		Items      []oauthGrantDocument `json:"items"`
		NextCursor string               `json:"nextCursor"`
	}
	if json.Unmarshal(first.Body.Bytes(), &page) != nil || len(page.Items) != 2 || page.NextCursor == "" ||
		len(page.Items[0].Scopes) != 2 {
		t.Fatalf("page=%+v body=%s", page, first.Body.String())
	}
	second := authenticatedGET(handler, cookie, "/oauth-grants?limit=2&cursor="+page.NextCursor)
	var next struct {
		Items []oauthGrantDocument `json:"items"`
	}
	if second.Code != http.StatusOK || json.Unmarshal(second.Body.Bytes(), &next) != nil || len(next.Items) != 1 {
		t.Fatalf("status=%d body=%s", second.Code, second.Body.String())
	}
	invalid := authenticatedGET(handler, cookie, "/oauth-grants?status=unknown")
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid status=%d body=%s", invalid.Code, invalid.Body.String())
	}
}

func TestNamespaceAdminListsOnlyExplicitNamespaceBeforeObjectLookup(t *testing.T) {
	handler, store, identity := newNamespaceTokenHandler(t, "payments")
	cookie := exchangeManagementToken(t, handler)
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Minute)
	for index, namespace := range []string{"payments", "other"} {
		session := storage.Session{
			ID: uuid.NewString(), IdentityID: identity.ID, DeviceID: "device", ClusterID: "cluster",
			Namespace: namespace, State: "active", CreatedAt: now.Add(time.Duration(index) * time.Second),
			ExpiresAt: now.Add(time.Hour),
		}
		if err := store.Sessions().Create(ctx, session); err != nil {
			t.Fatal(err)
		}
		if err := store.Tasks().Create(ctx, storage.Task{
			ID: uuid.NewString(), IdentityID: identity.ID, SessionID: session.ID, Type: "preview",
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
	bootstrap := authenticatedGET(handler, cookie, "/bootstrap")
	if bootstrap.Code != http.StatusOK || !strings.Contains(bootstrap.Body.String(), `"namespaces":["payments"]`) ||
		!strings.Contains(bootstrap.Body.String(), `"administrator":false`) || strings.Contains(bootstrap.Body.String(), `"other"`) {
		t.Fatalf("namespace authorization summary status=%d body=%s", bootstrap.Code, bootstrap.Body.String())
	}
}

func TestManagementRelayListPaginatesAndRedactsTransportIdentity(t *testing.T) {
	handler, _ := newIdentityTokenHandler(t, false, WithRelayStatusSource(relayStatusStub{statuses: []relayregistry.RelayStatus{
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

func exchangeManagementToken(t *testing.T, handler *Handler) *http.Cookie {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/sessions/token", bytes.NewBufferString(`{}`))
	request.RemoteAddr = "192.0.2.40:5000"
	request.Header.Set("Origin", "https://gateway.example")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer valid-access-token")
	recorder := httptest.NewRecorder()
	serveHTTP(handler, recorder, request)
	if recorder.Code != http.StatusCreated || len(recorder.Result().Cookies()) != 2 {
		t.Fatalf("token exchange status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	return recorder.Result().Cookies()[0]
}

func authenticatedGET(handler *Handler, cookie *http.Cookie, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()
	serveHTTP(handler, recorder, request)
	return recorder
}

func newNamespaceTokenHandler(t *testing.T, namespace string) (*Handler, *storage.Store, storage.Identity) {
	t.Helper()
	store, err := storage.Open(context.Background(), storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "management.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	identity, err := store.Identities().Create(context.Background(), storage.Identity{
		ID: uuid.NewString(), Type: "human", DisplayName: "Test Identity", Status: "active", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	authorizationID := uuid.NewString()
	if err := store.OAuthSessions().Create(context.Background(), storage.OAuthSession{
		Kind: "refresh_token", SignatureHash: bytes.Repeat([]byte{24}, 32), RequestID: authorizationID,
		RequestJSON: []byte(`{}`), Status: "active", CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	sessions, err := adminsession.New(store)
	if err != nil {
		t.Fatal(err)
	}
	organizationID := uuid.NewString()
	if err := store.Organizations().Create(context.Background(), storage.Organization{ID: organizationID, Name: "Test Organization", Slug: "test-organization", Status: "active", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.Organizations().AddMember(context.Background(), storage.OrganizationMembership{OrganizationID: organizationID, IdentityID: identity.ID, Status: "active", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	groupID := uuid.NewString()
	if err := store.Groups().Create(context.Background(), storage.Group{ID: groupID, OrganizationID: organizationID, Name: "Namespace users", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.Groups().AddMember(context.Background(), storage.GroupMembership{GroupID: groupID, IdentityID: identity.ID, SourceType: "manual", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.Groups().PutNamespace(context.Background(), storage.GroupNamespace{GroupID: groupID, Namespace: namespace, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	authorizer, err := adminauthorization.New(adminauthorization.Snapshot{
		Version: adminauthorization.CurrentVersion,
		Groups:  []adminauthorization.GroupAccess{{GroupID: groupID, Namespaces: []string{namespace}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(
		Config{PublicURL: "https://gateway.example"}, sessions,
		WithReadAPI(authorizer, store, BuildInfo{Version: "test", Commit: "test", ProtocolMin: "2.0", ProtocolMax: "2.0"}),
		WithTokenExchange(tokenAuthenticatorStub{identity: authn.AccessIdentity{
			Identity: identity, Groups: []string{groupID}, AuthorizationID: authorizationID, DeviceID: "browser", AccessExpiresAt: now.Add(time.Minute),
		}}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return handler, store, identity
}
