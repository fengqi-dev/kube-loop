package mirrorapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controller"
	"github.com/fengqi-dev/kube-loop/internal/controller/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controller/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/servicebinding"
	"github.com/google/uuid"
)

type mirrorTestSessions struct{ session sessionapi.ActiveSession }

func (sessions mirrorTestSessions) RequireActive(
	_ context.Context,
	_ controller.Principal,
	namespace, sessionID string,
) (sessionapi.ActiveSession, *controller.APIError) {
	if namespace != sessions.session.Namespace || sessionID != sessions.session.ID {
		return sessionapi.ActiveSession{}, notFound()
	}
	return sessions.session, nil
}

type mirrorTestServices struct{ calls int }

type mirrorTestResources struct{}

func (mirrorTestResources) Capture(context.Context, controller.Principal, *servicebinding.ServiceInterceptSnapshot) error {
	return nil
}

func (mirrorTestResources) Apply(context.Context, controller.Principal, servicebinding.ServiceInterceptSnapshot, string) error {
	return nil
}

func (mirrorTestResources) Restore(context.Context, servicebinding.ServiceInterceptSnapshot, string) error {
	return nil
}

func (services *mirrorTestServices) ResolveService(
	_ context.Context,
	_ controller.Principal,
	_, name string,
	ports []Port,
) (Service, error) {
	services.calls++
	return Service{Name: name, ClusterIP: "10.96.0.20", Ports: append([]Port(nil), ports...)}, nil
}

func TestMirrorTaskIsOwnedIdempotentAndDurablyStopped(t *testing.T) {
	ctx := context.Background()
	stateStore, err := storage.Open(ctx, storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "mirror.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stateStore.Close() })
	now := time.Now().UTC()
	principalID, sessionID := uuid.NewString(), uuid.NewString()
	if _, err := stateStore.Principals().Upsert(ctx, storage.Principal{
		ID: principalID, Provider: "test", ExternalID: "mirror-user", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	network, err := networkspec.Normalize(networkspec.Spec{ServiceIPs: []string{"10.96.0.10"}})
	if err != nil {
		t.Fatal(err)
	}
	networkJSON, err := networkspec.CanonicalJSON(network)
	if err != nil {
		t.Fatal(err)
	}
	networkHash, err := networkspec.Hash(network)
	if err != nil {
		t.Fatal(err)
	}
	if err := stateStore.Sessions().Create(ctx, storage.Session{
		ID: sessionID, PrincipalID: principalID, DeviceID: "device", ClusterID: "cluster",
		Namespace: "development", State: "active", Generation: 1,
		NetworkSpec: networkJSON, NetworkSpecHash: networkHash,
		CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	active := sessionapi.ActiveSession{ID: sessionID, Namespace: "development", Generation: 1, ExpiresAt: now.Add(time.Hour)}
	services := &mirrorTestServices{}
	handler, err := New(
		stateStore, mirrorTestSessions{session: active}, services, mirrorTestResources{},
		Config{GatewayIP: "127.0.0.1", Now: func() time.Time { return now }},
	)
	if err != nil {
		t.Fatal(err)
	}
	principal := controller.Principal{Subject: principalID, DeviceID: "device"}
	path := "/api/v2/sessions/" + sessionID + "/mirrors?namespace=development"
	body := []byte(`{"service":"api","ports":[{"servicePort":53,"protocol":"udp"},{"servicePort":80,"protocol":"tcp"}]}`)
	created, apiError := mirrorRequest(handler, principal, http.MethodPost, path, body, "mirror-1")
	if apiError != nil || created.Code != http.StatusCreated {
		t.Fatalf("create Mirror: status=%d error=%#v body=%s", created.Code, apiError, created.Body.String())
	}
	var document Document
	if err := json.Unmarshal(created.Body.Bytes(), &document); err != nil || document.ID == "" || document.State != "pending" || len(document.Ports) != 2 || document.Ports[0].ServicePort != 53 {
		t.Fatalf("created Mirror document=%#v err=%v", document, err)
	}
	replayed, apiError := mirrorRequest(handler, principal, http.MethodPost, path, body, "mirror-1")
	if apiError != nil || replayed.Code != http.StatusOK || replayed.Header().Get("Idempotent-Replayed") != "true" || services.calls != 1 {
		t.Fatalf("replay: status=%d error=%#v calls=%d", replayed.Code, apiError, services.calls)
	}
	mismatchBody := []byte(`{"service":"other","ports":[{"servicePort":80,"protocol":"tcp"}]}`)
	_, apiError = mirrorRequest(handler, principal, http.MethodPost, path, mismatchBody, "mirror-1")
	if apiError == nil || apiError.Code != controller.CodeConflict || services.calls != 1 {
		t.Fatalf("idempotency mismatch error=%#v calls=%d", apiError, services.calls)
	}
	taskPath := "/api/v2/sessions/" + sessionID + "/mirrors/" + document.ID + "?namespace=development"
	_, apiError = mirrorRequest(handler, controller.Principal{Subject: uuid.NewString(), DeviceID: "other"}, http.MethodGet, taskPath, nil, "")
	if apiError == nil || apiError.Code != controller.CodeNotFound {
		t.Fatalf("cross-principal get error=%#v", apiError)
	}
	stopped, apiError := mirrorRequest(handler, principal, http.MethodDelete, taskPath, nil, "")
	if apiError != nil || stopped.Code != http.StatusOK {
		t.Fatalf("stop pending Mirror: status=%d error=%#v", stopped.Code, apiError)
	}
	stored, err := stateStore.Tasks().GetByID(ctx, document.ID)
	if err != nil || stored.State != "stopped" {
		t.Fatalf("stored stopped Mirror=%#v err=%v", stored, err)
	}

	second, apiError := mirrorRequest(handler, principal, http.MethodPost, path, body, "mirror-2")
	if apiError != nil {
		t.Fatal(apiError)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.Tasks().UpdateState(ctx, document.ID, "pending", "running", json.RawMessage(`{}`), now); err != nil {
		t.Fatal(err)
	}
	taskPath = "/api/v2/sessions/" + sessionID + "/mirrors/" + document.ID + "?namespace=development"
	stopping, apiError := mirrorRequest(handler, principal, http.MethodDelete, taskPath, nil, "")
	if apiError != nil || stopping.Code != http.StatusAccepted {
		t.Fatalf("request running Mirror stop: status=%d error=%#v", stopping.Code, apiError)
	}
	stored, err = stateStore.Tasks().GetByID(ctx, document.ID)
	if err != nil || stored.State != "stopping" {
		t.Fatalf("stored stopping Mirror=%#v err=%v", stored, err)
	}
}

func mirrorRequest(
	handler *Handler,
	principal controller.Principal,
	method, path string,
	body []byte,
	idempotencyKey string,
) (*httptest.ResponseRecorder, *controller.APIError) {
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		request.Header.Set(sessionapi.IdempotencyHeader, idempotencyKey)
	}
	response := httptest.NewRecorder()
	return response, handler.ServeAPI(response, request, principal)
}
