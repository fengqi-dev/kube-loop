package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

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

func TestOperationsAPIEnforcesHeadersAndPersistsActions(t *testing.T) {
	handler, store, _ := newPolicyTestHandler(t)
	cookie, csrf := exchangeBreakGlassSession(t, handler)
	principal, session := seedOperationSession(t, store)

	missingCSRF := policyWrite(t, handler, cookie, "", "/sessions/"+session.ID+"/stop", `"1"`,
		"operation-http-stop-0001", map[string]string{"reason": "incident response"})
	if missingCSRF.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status=%d body=%s", missingCSRF.Code, missingCSRF.Body.String())
	}
	stopped := policyWrite(t, handler, cookie, csrf, "/sessions/"+session.ID+"/stop", `"1"`,
		"operation-http-stop-0001", map[string]string{"reason": "incident response"})
	if stopped.Code != http.StatusOK || stopped.Header().Get("ETag") != `"2"` ||
		!strings.Contains(stopped.Body.String(), `"runtimeConverged":true`) {
		t.Fatalf("Session stop status=%d headers=%v body=%s", stopped.Code, stopped.Header(), stopped.Body.String())
	}
	replay := policyWrite(t, handler, cookie, csrf, "/sessions/"+session.ID+"/stop", `"1"`,
		"operation-http-stop-0001", map[string]string{"reason": "incident response"})
	if replay.Code != http.StatusOK || replay.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("Session stop replay status=%d headers=%v body=%s", replay.Code, replay.Header(), replay.Body.String())
	}

	family := storage.TokenFamily{
		ID: uuid.NewString(), PrincipalID: principal.ID, DeviceID: "browser",
		RefreshTokenHash: bytes.Repeat([]byte{7}, 32), CreatedAt: time.Now().UTC().Add(-time.Hour), ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	if err := store.TokenFamilies().Create(context.Background(), family); err != nil {
		t.Fatal(err)
	}
	revoked := policyWrite(t, handler, cookie, csrf, "/principals/"+principal.ID+"/revoke", "",
		"operation-http-revoke-01", map[string]string{"reason": "disable compromised identity"})
	if revoked.Code != http.StatusOK || !strings.Contains(revoked.Body.String(), `"revokedCount":1`) {
		t.Fatalf("Principal revoke status=%d body=%s", revoked.Code, revoked.Body.String())
	}
	loaded, err := store.TokenFamilies().GetByID(context.Background(), family.ID)
	if err != nil || loaded.RevokedAt == nil {
		t.Fatalf("revoked Device Session = %#v, %v", loaded, err)
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

	recovery := policyWrite(t, handler, cookie, csrf, "/tasks/recovery", "",
		"operation-http-recover-01", map[string]string{"reason": "reconcile stale resources"})
	if recovery.Code != http.StatusOK || !strings.Contains(recovery.Body.String(), `"preview":0`) {
		t.Fatalf("recovery status=%d body=%s", recovery.Code, recovery.Body.String())
	}
	export := policyWrite(t, handler, cookie, csrf, "/audit/exports", "",
		"operation-http-export-001", map[string]any{"action": "admin.session.stop", "limit": 10, "reason": "export incident evidence"})
	if export.Code != http.StatusAccepted || export.Header().Get("Location") == "" {
		t.Fatalf("audit export status=%d headers=%v body=%s", export.Code, export.Header(), export.Body.String())
	}
	jobPath := strings.TrimPrefix(export.Header().Get("Location"), "/kubeloop/api/admin")
	pending := authenticatedGET(handler, cookie, jobPath)
	if pending.Code != http.StatusAccepted || pending.Header().Get("Retry-After") != "1" {
		t.Fatalf("pending audit export status=%d headers=%v body=%s", pending.Code, pending.Header(), pending.Body.String())
	}
}

func seedOperationSession(t *testing.T, store *storage.Store) (storage.Principal, storage.Session) {
	t.Helper()
	now := time.Now().UTC()
	principal, err := store.Principals().Upsert(context.Background(), storage.Principal{
		ID: uuid.NewString(), Provider: "oidc", ExternalID: uuid.NewString(), CreatedAt: now,
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
		ID: uuid.NewString(), PrincipalID: principal.ID, DeviceID: "device", ClusterID: "cluster",
		Namespace: "default", State: "active", Generation: 1, NetworkSpec: json.RawMessage(specJSON), NetworkSpecHash: specHash,
		CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: now.Add(time.Hour),
	}
	if err := store.Sessions().Create(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	return principal, session
}
