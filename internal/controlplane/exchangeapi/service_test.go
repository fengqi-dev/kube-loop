package exchangeapi

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

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/servicebinding"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficmodel"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
)

type exchangeTestSessions struct{ session sessionapi.ActiveSession }

func (sessions exchangeTestSessions) RequireActive(
	_ context.Context,
	_ controlplaneapi.Principal,
	namespace, sessionID string,
) (sessionapi.ActiveSession, *controlplaneapi.Error) {
	if namespace != sessions.session.Namespace || sessionID != sessions.session.ID {
		return sessionapi.ActiveSession{}, notFound()
	}
	return sessions.session, nil
}

type exchangeTestServices struct{ calls int }

type exchangeTestResources struct{}

func (exchangeTestResources) Capture(context.Context, controlplaneapi.Principal, *servicebinding.ServiceInterceptSnapshot) error {
	return nil
}

func (exchangeTestResources) Apply(context.Context, controlplaneapi.Principal, servicebinding.ServiceInterceptSnapshot, string) error {
	return nil
}

func (exchangeTestResources) Restore(context.Context, servicebinding.ServiceInterceptSnapshot, string) error {
	return nil
}

func (services *exchangeTestServices) ResolveService(
	_ context.Context,
	_ controlplaneapi.Principal,
	_, name string,
	ports []trafficmodel.Port,
) (trafficmodel.ResolvedService, error) {
	services.calls++
	return trafficmodel.ResolvedService{Name: name, ClusterIP: "10.96.0.20", Ports: append([]trafficmodel.Port(nil), ports...)}, nil
}

func TestExchangeTaskIsOwnedIdempotentAndDurablyStopped(t *testing.T) {
	ctx := context.Background()
	stateStore, err := storage.Open(ctx, storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "exchange.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stateStore.Close() })
	now := time.Now().UTC()
	principalID, sessionID := uuid.NewString(), uuid.NewString()
	if _, err := stateStore.Principals().Upsert(ctx, storage.Principal{
		ID: principalID, Provider: "test", ExternalID: "exchange-user", CreatedAt: now, UpdatedAt: now,
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
	services := &exchangeTestServices{}
	handler, err := New(
		stateStore, exchangeTestSessions{session: active}, services, exchangeTestResources{},
		Config{Now: func() time.Time { return now }},
	)
	if err != nil {
		t.Fatal(err)
	}
	principal := controlplaneapi.Principal{Subject: principalID, DeviceID: "device"}
	path := "/api/v2/sessions/" + sessionID + "/exchanges?namespace=development"
	body := []byte(`{"service":"api","ports":[{"servicePort":53,"protocol":"udp"},{"servicePort":80,"protocol":"tcp"}]}`)
	created, apiError := exchangeRequest(handler, principal, http.MethodPost, path, body, "exchange-1")
	if apiError != nil || created.Code != http.StatusCreated {
		t.Fatalf("create Exchange: status=%d error=%#v body=%s", created.Code, apiError, created.Body.String())
	}
	var document Document
	if err := json.Unmarshal(created.Body.Bytes(), &document); err != nil || document.ID == "" || document.State != "pending" || len(document.Ports) != 2 || document.Ports[0].ServicePort != 53 {
		t.Fatalf("created Exchange document=%#v err=%v", document, err)
	}
	replayed, apiError := exchangeRequest(handler, principal, http.MethodPost, path, body, "exchange-1")
	if apiError != nil || replayed.Code != http.StatusOK || replayed.Header().Get("Idempotent-Replayed") != "true" || services.calls != 1 {
		t.Fatalf("replay: status=%d error=%#v calls=%d", replayed.Code, apiError, services.calls)
	}
	mismatchBody := []byte(`{"service":"other","ports":[{"servicePort":80,"protocol":"tcp"}]}`)
	_, apiError = exchangeRequest(handler, principal, http.MethodPost, path, mismatchBody, "exchange-1")
	if apiError == nil || apiError.Code != controlplaneapi.CodeConflict || services.calls != 1 {
		t.Fatalf("idempotency mismatch error=%#v calls=%d", apiError, services.calls)
	}
	taskPath := "/api/v2/sessions/" + sessionID + "/exchanges/" + document.ID + "?namespace=development"
	_, apiError = exchangeRequest(handler, controlplaneapi.Principal{Subject: uuid.NewString(), DeviceID: "other"}, http.MethodGet, taskPath, nil, "")
	if apiError == nil || apiError.Code != controlplaneapi.CodeNotFound {
		t.Fatalf("cross-principal get error=%#v", apiError)
	}
	stopped, apiError := exchangeRequest(handler, principal, http.MethodDelete, taskPath, nil, "")
	if apiError != nil || stopped.Code != http.StatusOK {
		t.Fatalf("stop pending Exchange: status=%d error=%#v", stopped.Code, apiError)
	}
	stored, err := stateStore.Tasks().GetByID(ctx, document.ID)
	if err != nil || stored.State != "stopped" {
		t.Fatalf("stored stopped Exchange=%#v err=%v", stored, err)
	}

	second, apiError := exchangeRequest(handler, principal, http.MethodPost, path, body, "exchange-2")
	if apiError != nil {
		t.Fatal(apiError)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.Tasks().UpdateState(ctx, document.ID, "pending", "running", json.RawMessage(`{}`), now); err != nil {
		t.Fatal(err)
	}
	taskPath = "/api/v2/sessions/" + sessionID + "/exchanges/" + document.ID + "?namespace=development"
	stopping, apiError := exchangeRequest(handler, principal, http.MethodDelete, taskPath, nil, "")
	if apiError != nil || stopping.Code != http.StatusAccepted {
		t.Fatalf("request running Exchange stop: status=%d error=%#v", stopping.Code, apiError)
	}
	stored, err = stateStore.Tasks().GetByID(ctx, document.ID)
	if err != nil || stored.State != "stopping" {
		t.Fatalf("stored stopping Exchange=%#v err=%v", stored, err)
	}
}

func exchangeRequest(
	handler *Service,
	principal controlplaneapi.Principal,
	method, path string,
	body []byte,
	idempotencyKey string,
) (*httptest.ResponseRecorder, *controlplaneapi.Error) {
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		request.Header.Set(sessionapi.IdempotencyHeader, idempotencyKey)
	}
	response := httptest.NewRecorder()
	return response, serveAPI(handler, response, request, principal)
}

func serveAPI(handler *Service, writer http.ResponseWriter, request *http.Request, principal controlplaneapi.Principal) *controlplaneapi.Error {
	routes := NewRoutes(handler)
	parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	request.SetPathValue("sessionID", parts[3])
	if len(parts) > 5 {
		request.SetPathValue("taskID", parts[5])
	}
	switch {
	case request.Method == http.MethodPost:
		return routes.withSession(handler.create)(echo.New().NewContext(request, writer), principal)
	case request.Method == http.MethodDelete:
		return routes.withTask(handler.stop)(echo.New().NewContext(request, writer), principal)
	default:
		return routes.withTask(handler.get)(echo.New().NewContext(request, writer), principal)
	}
}
