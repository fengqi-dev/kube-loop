package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authorization"
	adminoperations "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/operations"
	adminrevision "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/revision"
	adminsession "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/session"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/google/uuid"
)

func TestPolicyAPICompletesDraftDryRunPublishAndRollback(t *testing.T) {
	handler, store, engine := newPolicyTestHandler(t)
	cookie, csrf := exchangeBreakGlassSession(t, handler)

	current := authenticatedGET(handler, cookie, "/policy")
	if current.Code != http.StatusOK || current.Header().Get("ETag") != `"0"` ||
		!strings.Contains(current.Body.String(), `"active":false`) {
		t.Fatalf("empty current policy status=%d headers=%v body=%s", current.Code, current.Header(), current.Body.String())
	}

	formalPrincipal := uuid.NewString()
	customPrincipal := uuid.NewString()
	firstSpec := policySpec{
		Version: adminauthorization.CurrentVersion,
		Roles: []adminauthorization.RoleDefinition{{
			ID: "session-reader", DisplayName: "Session reader", Permissions: []string{"admin.session/read"},
		}},
		Assignments: []adminauthorization.Assignment{
			{ID: uuid.NewString(), Role: adminauthorization.RolePlatformAdmin, Subjects: []string{formalPrincipal}},
			{ID: uuid.NewString(), Role: "session-reader", Subjects: []string{customPrincipal}},
		},
	}
	firstKey := "policy-http-create-0001"
	firstDraft := policyWrite(t, handler, cookie, csrf, "/policy/drafts", `"0"`, firstKey, map[string]any{
		"spec": firstSpec, "reason": "establish formal administrators",
	})
	if firstDraft.Code != http.StatusCreated {
		t.Fatalf("first draft status=%d body=%s", firstDraft.Code, firstDraft.Body.String())
	}
	var first struct {
		ChangeID string `json:"changeId"`
		Revision uint64 `json:"revision"`
	}
	if json.Unmarshal(firstDraft.Body.Bytes(), &first) != nil || first.ChangeID == "" || first.Revision == 0 {
		t.Fatalf("first draft body=%s", firstDraft.Body.String())
	}
	replay := policyWrite(t, handler, cookie, csrf, "/policy/drafts", `"0"`, firstKey, map[string]any{
		"spec": firstSpec, "reason": "establish formal administrators",
	})
	if replay.Code != http.StatusOK || replay.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("draft replay status=%d headers=%v body=%s", replay.Code, replay.Header(), replay.Body.String())
	}

	wrongKey := policyWrite(t, handler, cookie, csrf, "/policy/changes/"+first.ChangeID+"/publish", `"0"`,
		"policy-http-wrong-0001", map[string]any{"reason": "publish formal administrators"})
	if wrongKey.Code != http.StatusConflict {
		t.Fatalf("wrong publish key status=%d body=%s", wrongKey.Code, wrongKey.Body.String())
	}
	published := policyWrite(t, handler, cookie, csrf, "/policy/changes/"+first.ChangeID+"/publish", `"0"`,
		firstKey, map[string]any{"reason": "publish formal administrators"})
	if published.Code != http.StatusOK || published.Header().Get("ETag") != `"1"` ||
		engine.Revision() != first.Revision || engine.ETag() != 1 {
		t.Fatalf("first publish status=%d etag=%q engine=%d/%d body=%s",
			published.Code, published.Header().Get("ETag"), engine.Revision(), engine.ETag(), published.Body.String())
	}
	if decision := engine.Authorize(context.Background(), adminauthorization.Subject{ID: customPrincipal}, adminauthorization.Request{
		Resource: adminauthorization.ResourceSession, Operation: adminauthorization.OperationRead,
	}); !decision.Allowed || decision.Role != "session-reader" {
		t.Fatalf("custom role decision = %#v", decision)
	}
	publishReplay := policyWrite(t, handler, cookie, csrf, "/policy/changes/"+first.ChangeID+"/publish", `"0"`,
		firstKey, map[string]any{"reason": "retry formal administrator publish"})
	if publishReplay.Code != http.StatusOK || publishReplay.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("publish replay status=%d headers=%v body=%s", publishReplay.Code, publishReplay.Header(), publishReplay.Body.String())
	}

	dryRun := policyWrite(t, handler, cookie, csrf, "/policy/dry-run", `"1"`, "policy-http-dryrun-0001", map[string]any{
		"spec": firstSpec, "reason": "verify formal administrator access",
		"checks": []policyCheck{{
			Subject: struct {
				ID     string   `json:"id"`
				Groups []string `json:"groups,omitempty"`
			}{ID: formalPrincipal},
			Request: adminauthorization.Request{Resource: adminauthorization.ResourcePolicy, Operation: adminauthorization.OperationRead},
		}},
	})
	if dryRun.Code != http.StatusOK || !strings.Contains(dryRun.Body.String(), `"allowed":true`) ||
		!strings.Contains(dryRun.Body.String(), `"publishable":true`) {
		t.Fatalf("dry-run status=%d body=%s", dryRun.Code, dryRun.Body.String())
	}

	stale := policyWrite(t, handler, cookie, csrf, "/policy/drafts", `"0"`, "policy-http-stale-0001", map[string]any{
		"spec": firstSpec, "reason": "attempt a stale policy change",
	})
	if stale.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale draft status=%d body=%s", stale.Code, stale.Body.String())
	}

	secondSpec := firstSpec
	secondSpec.Assignments = append([]adminauthorization.Assignment(nil), firstSpec.Assignments...)
	secondSpec.Assignments = append(secondSpec.Assignments, adminauthorization.Assignment{
		ID: uuid.NewString(), Role: adminauthorization.RoleAuditor, Groups: []string{"auditors"},
	})
	secondKey := "policy-http-create-0002"
	secondDraft := policyWrite(t, handler, cookie, csrf, "/policy/drafts", `"1"`, secondKey, map[string]any{
		"spec": secondSpec, "reason": "add read only audit access",
	})
	var second struct {
		ChangeID string `json:"changeId"`
		Revision uint64 `json:"revision"`
	}
	if secondDraft.Code != http.StatusCreated || json.Unmarshal(secondDraft.Body.Bytes(), &second) != nil {
		t.Fatalf("second draft status=%d body=%s", secondDraft.Code, secondDraft.Body.String())
	}
	secondPublish := policyWrite(t, handler, cookie, csrf, "/policy/changes/"+second.ChangeID+"/publish", `"1"`,
		secondKey, map[string]any{"reason": "publish read only audit access"})
	if secondPublish.Code != http.StatusOK || engine.ETag() != 2 {
		t.Fatalf("second publish status=%d engine etag=%d body=%s", secondPublish.Code, engine.ETag(), secondPublish.Body.String())
	}
	rollback := policyWrite(t, handler, cookie, csrf, "/policy/rollback", `"2"`, "policy-http-rollback-01", map[string]any{
		"targetRevision": first.Revision, "reason": "restore the known good policy",
	})
	if rollback.Code != http.StatusOK || rollback.Header().Get("ETag") != `"3"` || engine.Revision() != first.Revision || engine.ETag() != 3 {
		t.Fatalf("rollback status=%d engine=%d/%d body=%s", rollback.Code, engine.Revision(), engine.ETag(), rollback.Body.String())
	}

	current = authenticatedGET(handler, cookie, "/policy")
	if current.Code != http.StatusOK || current.Header().Get("ETag") != `"3"` ||
		!strings.Contains(current.Body.String(), `"active":true`) ||
		!strings.Contains(current.Body.String(), `"id":"session-reader"`) ||
		!strings.Contains(current.Body.String(), `"admin.session/read"`) ||
		!strings.Contains(current.Body.String(), `"availablePermissions"`) {
		t.Fatalf("active current policy status=%d headers=%v body=%s", current.Code, current.Header(), current.Body.String())
	}
	events, err := store.Audit().List(context.Background(), storage.AuditFilter{Limit: 100})
	if err != nil || len(events) == 0 {
		t.Fatalf("policy audit events=%d error=%v", len(events), err)
	}
	for _, event := range events {
		if strings.Contains(string(event.Metadata), firstKey) || strings.Contains(string(event.Metadata), secondKey) {
			t.Fatalf("audit leaked plaintext idempotency key: %+v", event)
		}
	}
}

func TestPolicyAPIRequiresCSRFPreconditionIdempotencyAndStrictJSON(t *testing.T) {
	handler, _, _ := newPolicyTestHandler(t)
	cookie, csrf := exchangeBreakGlassSession(t, handler)
	body := map[string]any{"spec": policySpec{Version: 1, Assignments: []adminauthorization.Assignment{}}, "reason": "validate request boundaries"}

	missingCSRF := policyWrite(t, handler, cookie, "", "/policy/dry-run", `"0"`, "policy-boundary-key-01", body)
	if missingCSRF.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status=%d body=%s", missingCSRF.Code, missingCSRF.Body.String())
	}
	missingETag := policyWrite(t, handler, cookie, csrf, "/policy/dry-run", "", "policy-boundary-key-02", body)
	if missingETag.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing If-Match status=%d body=%s", missingETag.Code, missingETag.Body.String())
	}
	missingKey := policyWrite(t, handler, cookie, csrf, "/policy/dry-run", `"0"`, "", body)
	if missingKey.Code != http.StatusBadRequest {
		t.Fatalf("missing idempotency key status=%d body=%s", missingKey.Code, missingKey.Body.String())
	}
	unknown := policyWrite(t, handler, cookie, csrf, "/policy/dry-run", `"0"`, "policy-boundary-key-03", map[string]any{
		"spec": body["spec"], "reason": body["reason"], "unknown": true,
	})
	if unknown.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status=%d body=%s", unknown.Code, unknown.Body.String())
	}
}

func newPolicyTestHandler(t *testing.T) (*Handler, *storage.Store, *adminauthorization.Engine) {
	t.Helper()
	store, err := storage.Open(context.Background(), storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "management.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	generation := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))
	verifier := &testVerifier{enabled: true, generation: generation}
	sessions, err := adminsession.New(store, verifier)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := adminauthorization.NewDenyAll(adminauthorization.WithBreakGlass(verifier))
	if err != nil {
		t.Fatal(err)
	}
	loader, err := adminrevision.NewPolicyLoader(store, engine, 0)
	if err != nil {
		t.Fatal(err)
	}
	service, err := adminrevision.New(store)
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
		WithPolicyAPI(service, loader),
		WithOperationsAPI(operations),
	)
	if err != nil {
		t.Fatal(err)
	}
	return handler, store, engine
}

func exchangeBreakGlassSession(t *testing.T, handler *Handler) (*http.Cookie, string) {
	t.Helper()
	issued, err := handler.sessions.ExchangeBreakGlass(
		context.Background(), netip.MustParseAddr("192.0.2.20"), []byte("valid"), uuid.NewString(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: SessionCookieName, Value: issued.SessionToken}, issued.CSRFToken
}

func policyWrite(
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
