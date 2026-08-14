package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authorization"
	adminconfig "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/managementconfig"
	adminsession "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/session"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

type providerRuntimeStub struct {
	mu       sync.Mutex
	installs int
}

func (runtime *providerRuntimeStub) Validate(_ context.Context, candidate adminconfig.ProviderCandidate) (json.RawMessage, error) {
	return json.RawMessage(`{"valid":true,"connectivity":"ready"}`), nil
}

func (runtime *providerRuntimeStub) Prepare(_ context.Context, candidate adminconfig.ProviderCandidate) (func(), error) {
	return func() {
		runtime.mu.Lock()
		runtime.installs++
		runtime.mu.Unlock()
	}, nil
}

func TestProviderAPIValidatesPublishesAndRedacts(t *testing.T) {
	handler, store, runtime := newProviderTestHandler(t)
	cookie, csrf := exchangeBreakGlassSession(t, handler)
	current := authenticatedGET(handler, cookie, "/providers/corporate")
	if current.Code != http.StatusOK || !strings.Contains(current.Body.String(), `"active":false`) {
		t.Fatalf("empty Provider status=%d headers=%v body=%s", current.Code, current.Header(), current.Body.String())
	}

	first := map[string]any{
		"type":   "oidc",
		"config": map[string]any{"issuer": "https://issuer.example", "clientId": "kubeloop", "clientSecret": "corporate-secret", "claims": map[string]string{}},
		"reason": "configure corporate identity Provider",
	}
	validated := policyWrite(t, handler, cookie, csrf, "/providers/corporate/validate", "provider-validate-key-01", first)
	if validated.Code != http.StatusOK || !strings.Contains(validated.Body.String(), `"connectivity":"ready"`) {
		t.Fatalf("validation status=%d body=%s", validated.Code, validated.Body.String())
	}
	key := "provider-create-key-0001"
	draft := policyWrite(t, handler, cookie, csrf, "/providers/corporate/drafts", key, first)
	var created struct {
		ChangeID string `json:"changeId"`
		ObjectID string `json:"objectId"`
	}
	if draft.Code != http.StatusCreated || json.Unmarshal(draft.Body.Bytes(), &created) != nil || created.ChangeID == "" ||
		strings.Contains(draft.Body.String(), "corporate-secret") {
		t.Fatalf("draft status=%d body=%s", draft.Code, draft.Body.String())
	}
	published := policyWrite(t, handler, cookie, csrf, "/providers/corporate/changes/"+created.ChangeID+"/publish", key,
		map[string]any{"reason": "publish corporate identity Provider"})
	if published.Code != http.StatusOK {
		t.Fatalf("publish status=%d headers=%v body=%s", published.Code, published.Header(), published.Body.String())
	}
	current = authenticatedGET(handler, cookie, "/providers/corporate")
	if current.Code != http.StatusOK || !strings.Contains(current.Body.String(), `"clientSecretConfigured":true`) ||
		strings.Contains(current.Body.String(), "corporate-secret") {
		t.Fatalf("current Provider status=%d body=%s", current.Code, current.Body.String())
	}
	listed := authenticatedGET(handler, cookie, "/providers")
	if listed.Code != http.StatusOK || strings.Contains(listed.Body.String(), "corporate-secret") {
		t.Fatalf("Provider list status=%d body=%s", listed.Code, listed.Body.String())
	}

	second := first
	second["config"] = map[string]any{"issuer": "https://issuer.example", "clientId": "kubeloop-v2", "claims": map[string]string{}}
	second["reason"] = "rotate corporate Provider client configuration"
	secondKey := "provider-create-key-0002"
	secondDraft := policyWrite(t, handler, cookie, csrf, "/providers/corporate/drafts", secondKey, second)
	var next struct {
		ChangeID string `json:"changeId"`
	}
	if secondDraft.Code != http.StatusCreated || json.Unmarshal(secondDraft.Body.Bytes(), &next) != nil {
		t.Fatalf("second draft status=%d body=%s", secondDraft.Code, secondDraft.Body.String())
	}
	secondPublish := policyWrite(t, handler, cookie, csrf, "/providers/corporate/changes/"+next.ChangeID+"/publish", secondKey,
		map[string]any{"reason": "publish rotated Provider client configuration"})
	if secondPublish.Code != http.StatusOK {
		t.Fatalf("second publish status=%d body=%s", secondPublish.Code, secondPublish.Body.String())
	}
	runtime.mu.Lock()
	installs := runtime.installs
	runtime.mu.Unlock()
	if installs != 2 {
		t.Fatalf("runtime installs=%d", installs)
	}
	events, err := store.Audit().List(context.Background(), storage.AuditFilter{Limit: 100})
	if err != nil || len(events) == 0 {
		t.Fatalf("Provider audit events=%d error=%v", len(events), err)
	}
	for _, event := range events {
		if strings.Contains(string(event.Metadata), "corporate-secret") || strings.Contains(string(event.Metadata), key) {
			t.Fatalf("Provider audit leaked Secret alias or idempotency key: %+v", event)
		}
	}
}

func newProviderTestHandler(t *testing.T) (*Handler, *storage.Store, *providerRuntimeStub) {
	t.Helper()
	store, err := storage.Open(context.Background(), storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "management.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	generation := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	verifier := &testVerifier{enabled: true, generation: generation}
	sessions, err := adminsession.New(store, verifier)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := adminauthorization.NewDenyAll(adminauthorization.WithBreakGlass(verifier))
	if err != nil {
		t.Fatal(err)
	}
	runtime := &providerRuntimeStub{}
	service, err := adminconfig.NewProviderService(store, runtime, runtime)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(Config{PublicURL: "https://gateway.example"}, sessions,
		WithReadAPI(engine, store, BuildInfo{Version: "test", Commit: "test", ProtocolMin: "2.0", ProtocolMax: "2.0"}),
		WithProviderAPI(service),
	)
	if err != nil {
		t.Fatal(err)
	}
	return handler, store, runtime
}
