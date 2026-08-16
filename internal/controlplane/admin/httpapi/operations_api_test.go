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
	adminoperations "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/operations"
	adminsession "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/session"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/google/uuid"
)

type noopSessionRuntime struct{}

func (noopSessionRuntime) Disconnect(context.Context, string) error { return nil }

type noopRecoveryRunner struct{}

func (noopRecoveryRunner) RunOnce(context.Context) (map[string]int, error) {
	return map[string]int{"preview": 0}, nil
}

func newPolicyTestHandler(t *testing.T) (*Handler, *storage.Store, *adminauthorization.Engine) {
	t.Helper()
	store, err := storage.Open(context.Background(), storage.Config{Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "management.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	sessions, err := adminsession.New(store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	organizationID, groupID := uuid.NewString(), uuid.NewString()
	if _, err = store.Identities().Create(context.Background(), storage.Identity{ID: testManagementIdentityID, Type: "human", DisplayName: "Test administrator", Status: "active", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err = store.OAuthSessions().Create(context.Background(), storage.OAuthSession{Kind: "refresh_token", SignatureHash: bytes.Repeat([]byte{30}, 32), RequestID: testManagementAuthorizationID, IdentityID: testManagementIdentityID, RequestJSON: []byte(`{}`), Status: "active", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err = store.Organizations().Create(context.Background(), storage.Organization{ID: organizationID, Name: "Operations", Slug: "operations", Status: "active", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err = store.Organizations().AddMember(context.Background(), storage.OrganizationMembership{OrganizationID: organizationID, IdentityID: testManagementIdentityID, Status: "active", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err = store.Groups().Create(context.Background(), storage.Group{ID: groupID, OrganizationID: organizationID, Name: "Administrators", System: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err = store.Groups().AddMember(context.Background(), storage.GroupMembership{GroupID: groupID, IdentityID: testManagementIdentityID, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	engine, err := adminauthorization.New(adminauthorization.Snapshot{Version: adminauthorization.CurrentVersion, Groups: []adminauthorization.GroupAccess{{GroupID: groupID, Administrator: true}}})
	if err != nil {
		t.Fatal(err)
	}
	operations, err := adminoperations.New(store, noopSessionRuntime{})
	if err != nil {
		t.Fatal(err)
	}
	if err := operations.ConfigureRecovery(noopRecoveryRunner{}); err != nil {
		t.Fatal(err)
	}
	handler, err := New(Config{PublicURL: "https://gateway.example"}, sessions,
		WithReadAPI(engine, store, BuildInfo{Version: "test", Commit: "test", ProtocolMin: "2.0", ProtocolMax: "2.0"}),
		WithOperationsAPI(operations))
	if err != nil {
		t.Fatal(err)
	}
	return handler, store, engine
}

func TestOperationsAPIEnforcesHeadersAndPersistsActions(t *testing.T) {
	handler, store, _ := newPolicyTestHandler(t)
	cookie, csrf := issueTestSession(t, handler, store)
	identity, session := seedOperationSession(t, store)

	missingCSRF := operationWrite(t, handler, cookie, "", "/sessions/"+session.ID+"/stop", `"1"`,
		"operation-http-stop-0001", map[string]string{"reason": "incident response"})
	if missingCSRF.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status=%d body=%s", missingCSRF.Code, missingCSRF.Body.String())
	}
	stopped := operationWrite(t, handler, cookie, csrf, "/sessions/"+session.ID+"/stop", `"1"`,
		"operation-http-stop-0001", map[string]string{"reason": "incident response"})
	if stopped.Code != http.StatusOK || stopped.Header().Get("ETag") != `"2"` ||
		!strings.Contains(stopped.Body.String(), `"runtimeConverged":true`) {
		t.Fatalf("Session stop status=%d headers=%v body=%s", stopped.Code, stopped.Header(), stopped.Body.String())
	}
	replay := operationWrite(t, handler, cookie, csrf, "/sessions/"+session.ID+"/stop", `"1"`,
		"operation-http-stop-0001", map[string]string{"reason": "incident response"})
	if replay.Code != http.StatusOK || replay.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("Session stop replay status=%d headers=%v body=%s", replay.Code, replay.Header(), replay.Body.String())
	}

	grant := storage.OAuthSession{
		Kind: "refresh_token", SignatureHash: bytes.Repeat([]byte{7}, 32), RequestID: uuid.NewString(), IdentityID: identity.ID,
		ClientID: "desktop", DeviceID: "browser", RequestJSON: []byte(`{}`), Status: "active", CreatedAt: time.Now().UTC().Add(-time.Hour), ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	if err := store.OAuthSessions().Create(context.Background(), grant); err != nil {
		t.Fatal(err)
	}
	revokedGrant := operationWrite(t, handler, cookie, csrf,
		"/identities/"+identity.ID+"/oauth-grants/"+grant.RequestID+"/revoke", "",
		"operation-http-grant-001", map[string]string{"reason": "revoke compromised authorization"})
	if revokedGrant.Code != http.StatusOK ||
		!strings.Contains(revokedGrant.Body.String(), `"authorizationId":"`+grant.RequestID+`"`) ||
		strings.Contains(revokedGrant.Body.String(), "deviceSessionId") {
		t.Fatalf("OAuth grant revoke status=%d body=%s", revokedGrant.Code, revokedGrant.Body.String())
	}
	active, err := store.OAuthSessions().RequestActive(context.Background(), grant.RequestID, time.Now().UTC())
	if err != nil || active {
		t.Fatalf("revoked OAuth grant active=%v, %v", active, err)
	}
	grant.SignatureHash = bytes.Repeat([]byte{8}, 32)
	grant.RequestID = uuid.NewString()
	if err := store.OAuthSessions().Create(context.Background(), grant); err != nil {
		t.Fatal(err)
	}
	revoked := operationWrite(t, handler, cookie, csrf, "/identities/"+identity.ID+"/revoke", "",
		"operation-http-revoke-01", map[string]string{"reason": "disable compromised identity"})
	if revoked.Code != http.StatusOK || !strings.Contains(revoked.Body.String(), `"revokedCount":1`) {
		t.Fatalf("Identity revoke status=%d body=%s", revoked.Code, revoked.Body.String())
	}
	active, err = store.OAuthSessions().RequestActive(context.Background(), grant.RequestID, time.Now().UTC())
	if err != nil || active {
		t.Fatalf("revoked OAuth grant active=%v, %v", active, err)
	}
	events, err := store.Audit().List(context.Background(), storage.AuditFilter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if strings.Contains(string(event.Metadata), "operation-http-") {
			t.Fatalf("audit leaked plaintext idempotency key: %#v", event)
		}
	}

	recovery := operationWrite(t, handler, cookie, csrf, "/tasks/recovery", "",
		"operation-http-recover-01", map[string]string{"reason": "reconcile stale resources"})
	if recovery.Code != http.StatusOK || !strings.Contains(recovery.Body.String(), `"preview":0`) {
		t.Fatalf("recovery status=%d body=%s", recovery.Code, recovery.Body.String())
	}
	export := operationWrite(t, handler, cookie, csrf, "/audit/exports", "",
		"operation-http-export-001", map[string]any{"action": "admin.session.stop", "limit": 10, "reason": "export incident evidence"})
	if export.Code != http.StatusAccepted || export.Header().Get("Location") == "" {
		t.Fatalf("audit export status=%d headers=%v body=%s", export.Code, export.Header(), export.Body.String())
	}
	jobPath := strings.TrimPrefix(export.Header().Get("Location"), "/api/admin")
	pending := authenticatedGET(handler, cookie, jobPath)
	if pending.Code != http.StatusAccepted || pending.Header().Get("Retry-After") != "1" {
		t.Fatalf("pending audit export status=%d headers=%v body=%s", pending.Code, pending.Header(), pending.Body.String())
	}
}

func operationWrite(
	t *testing.T,
	handler *Handler,
	cookie *http.Cookie,
	csrf, path, etag, key string,
	body any,
) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(encoded))
	request.AddCookie(cookie)
	request.Header.Set("Origin", "https://gateway.example")
	request.Header.Set("Content-Type", "application/json")
	if csrf != "" {
		request.Header.Set(CSRFHeaderName, csrf)
	}
	if etag != "" {
		request.Header.Set("If-Match", etag)
	}
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	recorder := httptest.NewRecorder()
	serveHTTP(handler, recorder, request)
	return recorder
}

func seedOperationSession(t *testing.T, store *storage.Store) (storage.Identity, storage.Session) {
	t.Helper()
	now := time.Now().UTC()
	identity, err := store.Identities().Create(context.Background(), storage.Identity{
		ID: uuid.NewString(), Type: "human", DisplayName: "Test Identity", Status: "active", CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := networkspec.Normalize(networkspec.Spec{
		PodCIDRs: []string{"10.2.0.0/16"}, ServiceCIDRs: []string{"10.96.0.0/12"},
		ServiceIPs: []string{"10.96.0.10"}, DNSServer: "10.96.0.10",
	})
	if err != nil {
		t.Fatal(err)
	}
	specJSON, _ := networkspec.CanonicalJSON(spec)
	specHash, _ := networkspec.Hash(spec)
	session := storage.Session{
		ID: uuid.NewString(), IdentityID: identity.ID, DeviceID: "device", ClusterID: "cluster",
		Namespace: "default", State: "active", Generation: 1, NetworkSpec: json.RawMessage(specJSON), NetworkSpecHash: specHash,
		CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: now.Add(time.Hour),
	}
	if err := store.Sessions().Create(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	return identity, session
}
