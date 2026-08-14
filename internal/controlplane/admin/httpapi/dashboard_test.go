package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/relayregistry"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relaycontrol"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
	"github.com/google/uuid"
)

func TestBootstrapRequiresAuthenticationAndConsolidatesSessionAuthorization(t *testing.T) {
	handler, _ := newPrincipalTokenHandler(t, false)
	unauthenticated := httptest.NewRecorder()
	serveHTTP(handler, unauthenticated, httptest.NewRequest(http.MethodGet, "/bootstrap", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d body=%s", unauthenticated.Code, unauthenticated.Body.String())
	}

	cookie := exchangeManagementToken(t, handler)
	response := authenticatedGET(handler, cookie, "/bootstrap")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var document bootstrapDocument
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if document.Identity.ID == "" || len(document.Identity.Groups) != 1 || document.Identity.Groups[0] != "platform" {
		t.Fatalf("identity=%+v", document.Identity)
	}
	if document.Session.AuthenticationType != "normal" || document.Session.CreatedAt.IsZero() ||
		document.Session.AbsoluteExpiresAt.IsZero() {
		t.Fatalf("session=%+v", document.Session)
	}
	if len(document.Authorization.Capabilities) == 0 {
		t.Fatalf("authorization=%+v", document.Authorization)
	}
}

func TestBootstrapRemainsAvailableWithoutPrivilegesButOverviewRequiresStatusRead(t *testing.T) {
	handler, _ := newReadTestHandler(t, false)
	cookie, _ := issueLegacySession(t, handler)
	bootstrap := authenticatedGET(handler, cookie, "/bootstrap")
	if bootstrap.Code != http.StatusOK {
		t.Fatalf("bootstrap status=%d body=%s", bootstrap.Code, bootstrap.Body.String())
	}
	var document bootstrapDocument
	if err := json.Unmarshal(bootstrap.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if document.Identity.Groups == nil || len(document.Authorization.Capabilities) != 0 {
		t.Fatalf("bootstrap document=%+v", document)
	}
	overview := authenticatedGET(handler, cookie, "/overview")
	if overview.Code != http.StatusForbidden {
		t.Fatalf("overview status=%d body=%s", overview.Code, overview.Body.String())
	}
}

func TestOverviewReturnsBoundedRuntimeAndRelaySummary(t *testing.T) {
	handler, store := newPrincipalTokenHandler(t, false, WithRelayStatusSource(relayStatusStub{statuses: []relayregistry.RelayStatus{
		{
			RelayID: "relay-ready", State: relaycontrol.StateReady, DesiredState: relaycontrol.StateReady,
			Online: true, Reservations: 2,
		},
		{
			RelayID: "relay-draining", State: relaycontrol.StateDraining, DesiredState: relaycontrol.StateDraining,
			Reservations: 1,
		},
	}}))
	cookie := exchangeManagementToken(t, handler)
	now := time.Now().UTC().Add(-time.Minute)
	principal, err := store.Principals().Upsert(context.Background(), storage.Principal{
		ID: uuid.NewString(), Provider: "oidc", ExternalID: "overview-owner", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	session := storage.Session{
		ID: uuid.NewString(), PrincipalID: principal.ID, DeviceID: "device", ClusterID: "cluster",
		Namespace: "overview", State: "active", CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	if err := store.Sessions().Create(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if err := store.Tasks().Create(context.Background(), storage.Task{
		ID: uuid.NewString(), PrincipalID: principal.ID, SessionID: session.ID, Type: "pod-exec",
		State: remotetask.Running, Spec: json.RawMessage(`{}`), Result: json.RawMessage(`{}`),
		IdempotencyKey: "overview-task", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	response := authenticatedGET(handler, cookie, "/overview")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var document struct {
		System struct {
			Storage struct {
				Status string `json:"status"`
			} `json:"storage"`
		} `json:"system"`
		Runtime struct {
			ActiveSessions overviewCountDocument `json:"activeSessions"`
			ActiveTasks    overviewCountDocument `json:"activeTasks"`
			Relays         overviewRelayDocument `json:"relays"`
		} `json:"runtime"`
		RecentAudit []auditDocument `json:"recentAudit"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if document.System.Storage.Status != "ready" || document.Runtime.ActiveSessions.Count != 1 ||
		document.Runtime.ActiveTasks.Count != 1 || document.Runtime.ActiveSessions.Truncated ||
		document.Runtime.ActiveTasks.Truncated {
		t.Fatalf("system=%+v runtime=%+v", document.System, document.Runtime)
	}
	if document.Runtime.Relays.Total != 2 || document.Runtime.Relays.Online != 1 ||
		document.Runtime.Relays.Ready != 1 || document.Runtime.Relays.Draining != 1 ||
		document.Runtime.Relays.Reservations != 3 {
		t.Fatalf("relays=%+v", document.Runtime.Relays)
	}
	if len(document.RecentAudit) == 0 {
		t.Fatal("authorized overview omitted recent audit events")
	}
}
