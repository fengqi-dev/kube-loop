package portforwardapi_test

import (
	"bytes"
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
	"github.com/fengqi-dev/kube-loop/internal/controlplane/portforwardapi"
	portforwardservice "github.com/fengqi-dev/kube-loop/internal/controlplane/portforwardapi/service"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/google/uuid"
)

type ownedSessionValidator struct {
	identityID string
	session    sessionapi.ActiveSession
}

func (validator ownedSessionValidator) RequireActive(
	_ context.Context,
	identity controlplaneapi.Identity,
	namespace, sessionID string,
) (sessionapi.ActiveSession, *controlplaneapi.Error) {
	if identity.Subject != validator.identityID || namespace != validator.session.Namespace || sessionID != validator.session.ID {
		return sessionapi.ActiveSession{}, &controlplaneapi.Error{Code: controlplaneapi.CodeNotFound, Message: "resource not found"}
	}
	return validator.session, nil
}

type captureResolver struct {
	mu    sync.Mutex
	calls []portforwardservice.Spec
}

type bindingManager struct{}

func (bindingManager) Activate(context.Context, sessionapi.ActiveSession, string, portforwardservice.Spec) (bool, error) {
	return true, nil
}

func (bindingManager) Delete(context.Context, string, string) error { return nil }

func (resolver *captureResolver) Resolve(
	_ context.Context,
	_ controlplaneapi.Identity,
	_ string,
	spec portforwardservice.Spec,
) (portforwardservice.Target, error) {
	resolver.mu.Lock()
	resolver.calls = append(resolver.calls, spec)
	resolver.mu.Unlock()
	return portforwardservice.Target{Host: "10.96.0.20", Port: spec.RemotePort}, nil
}

func TestPortForwardTaskLifecycleIsOwnedIdempotentAndPolicyMapped(t *testing.T) {
	now := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	stateStore, err := storage.Open(context.Background(), storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "controlplane.db"), ControlPlaneReplicas: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stateStore.Close()
	identityID := uuid.NewString()
	sessionID := uuid.NewString()
	if _, err := stateStore.Identities().Create(context.Background(), storage.Identity{
		ID: identityID, Type: "human", DisplayName: "Test Identity", Status: "active", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	spec, err := networkspec.Normalize(networkspec.Spec{
		PodCIDRs: []string{"10.244.0.0/16"}, ServiceCIDRs: []string{"10.96.0.0/12"},
		ServiceIPs: []string{"10.96.0.10"}, DNSServer: "10.96.0.10",
	})
	if err != nil {
		t.Fatal(err)
	}
	specJSON, _ := networkspec.CanonicalJSON(spec)
	specHash, _ := networkspec.Hash(spec)
	expiresAt := now.Add(time.Hour)
	if err := stateStore.Sessions().Create(context.Background(), storage.Session{
		ID: sessionID, IdentityID: identityID, DeviceID: "device", ClusterID: "cluster",
		Namespace: "development", State: "active", Generation: 1,
		NetworkSpec: specJSON, NetworkSpecHash: specHash,
		CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: expiresAt,
	}); err != nil {
		t.Fatal(err)
	}
	resolver := &captureResolver{}
	sessions := ownedSessionValidator{
		identityID: identityID,
		session: sessionapi.ActiveSession{
			ID: sessionID, Namespace: "development", Generation: 1, ExpiresAt: expiresAt, NetworkSpecHash: specHash,
		},
	}
	service, err := portforwardservice.New(stateStore, resolver, bindingManager{}, portforwardservice.Config{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := authorization.New(authorization.Policy{Rules: []authorization.Rule{{
		ID: "port-forward", Subjects: []string{"*"}, Namespaces: []string{"development"},
		Operations: []string{"create", "list", "delete"}, ResourceKinds: []string{"port-forwards"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	server, err := controlplane.NewServer(
		controlplane.Config{PublicURL: "https://gateway.example.test"}, controlplane.BuildInfo{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		controlplane.WithAuthenticator(controlplaneapi.AuthenticatorFunc(func(request *http.Request) (controlplaneapi.Identity, *controlplaneapi.Error) {
			return controlplaneapi.Identity{Subject: request.Header.Get("X-Identity"), DeviceID: "device"}, nil
		})),
		controlplane.WithAuthorizer(policy), controlplane.WithAPIRoutes(controlplane.APIRoutes{PortForwards: portforwardapi.NewRoutes(service, sessions).Endpoints()}),
	)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/sessions/" + sessionID + "/port-forwards?namespace=development"
	body := []byte(`{"kind":"service","name":"api","protocol":"tcp","remotePort":8443}`)
	created := taskRequest(t, server, http.MethodPost, path, identityID, "pf-1", body)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d body = %s", created.Code, created.Body.String())
	}
	document := decodeTaskDocument(t, created)
	if document.SessionID != sessionID || document.State != "running" || document.DialAddress != "10.96.0.20:8443" {
		t.Fatalf("created task = %#v", document)
	}
	replayed := taskRequest(t, server, http.MethodPost, path, identityID, "pf-1", body)
	if replayed.Code != http.StatusOK || replayed.Header().Get("Idempotent-Replayed") != "true" || decodeTaskDocument(t, replayed).ID != document.ID {
		t.Fatalf("replay status = %d headers = %#v body = %s", replayed.Code, replayed.Header(), replayed.Body.String())
	}
	resolver.mu.Lock()
	if len(resolver.calls) != 1 {
		t.Fatalf("idempotent replay resolved Kubernetes target %d times", len(resolver.calls))
	}
	resolver.mu.Unlock()
	mismatch := taskRequest(t, server, http.MethodPost, path, identityID, "pf-1", []byte(`{"kind":"service","name":"api","protocol":"tcp","remotePort":9443}`))
	if mismatch.Code != http.StatusConflict {
		t.Fatalf("mismatch status = %d body = %s", mismatch.Code, mismatch.Body.String())
	}
	listed := taskRequest(t, server, http.MethodGet, path, identityID, "", nil)
	var list struct {
		Items []portforwardapi.Document `json:"items"`
	}
	if listed.Code != http.StatusOK || json.Unmarshal(listed.Body.Bytes(), &list) != nil || len(list.Items) != 1 {
		t.Fatalf("list status = %d body = %s", listed.Code, listed.Body.String())
	}
	stopped := taskRequest(t, server, http.MethodDelete,
		"/api/sessions/"+sessionID+"/port-forwards/"+document.ID+"?namespace=development",
		identityID, "", nil,
	)
	if stopped.Code != http.StatusOK || decodeTaskDocument(t, stopped).State != "stopped" {
		t.Fatalf("stop status = %d body = %s", stopped.Code, stopped.Body.String())
	}
	foreign := taskRequest(t, server, http.MethodGet, path, uuid.NewString(), "", nil)
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("foreign status = %d body = %s", foreign.Code, foreign.Body.String())
	}
}

func taskRequest(
	t *testing.T,
	server *controlplane.Server,
	method, path, identityID, idempotencyKey string,
	body []byte,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	request.Header.Set("X-Identity", identityID)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		request.Header.Set(sessionapi.IdempotencyHeader, idempotencyKey)
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}

func decodeTaskDocument(t *testing.T, response *httptest.ResponseRecorder) portforwardapi.Document {
	t.Helper()
	var document portforwardapi.Document
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	return document
}
