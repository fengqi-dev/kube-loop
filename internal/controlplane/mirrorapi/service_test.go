package mirrorapi

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

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/servicebinding"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficmodel"
)

type mirrorTestSessions struct{ session sessionapi.ActiveSession }

func (sessions mirrorTestSessions) RequireActive(
	_ context.Context,
	_ controlplaneapi.Identity,
	namespace, sessionID string,
) (sessionapi.ActiveSession, *controlplaneapi.Error) {
	if namespace != sessions.session.Namespace ||
		sessionID != sessions.session.ID {
		return sessionapi.ActiveSession{}, notFound()
	}
	return sessions.session, nil
}

type mirrorTestServices struct{ calls int }

type mirrorTestResources struct{}

func (mirrorTestResources) Capture(
	context.Context,
	controlplaneapi.Identity,
	*servicebinding.ServiceInterceptSnapshot,
) error {
	return nil
}

func (mirrorTestResources) Apply(
	context.Context,
	controlplaneapi.Identity,
	servicebinding.ServiceInterceptSnapshot,
	string,
) error {
	return nil
}

func (mirrorTestResources) Restore(
	context.Context,
	servicebinding.ServiceInterceptSnapshot,
	string,
) error {
	return nil
}

func (services *mirrorTestServices) ResolveService(
	_ context.Context,
	_ controlplaneapi.Identity,
	_, name string,
	ports []trafficmodel.Port,
) (trafficmodel.ResolvedService, error) {
	services.calls++
	return trafficmodel.ResolvedService{
		Name:      name,
		ClusterIP: "10.96.0.20",
		Ports:     append([]trafficmodel.Port(nil), ports...),
	}, nil
}

func TestMirrorTaskIsOwnedIdempotentAndDurablyStopped(t *testing.T) {
	ctx, stateStore, now, identityID, sessionID, active := newMirrorLifecycleState(t)
	services := &mirrorTestServices{}
	handler, err := New(
		stateStore,
		mirrorTestSessions{session: active},
		services,
		mirrorTestResources{},
		Config{Now: func() time.Time { return now }},
	)
	if err != nil {
		t.Fatal(err)
	}
	identity := controlplaneapi.Identity{
		Subject:  identityID,
		DeviceID: "device",
	}
	path := "/api/sessions/" + sessionID + "/mirrors?namespace=development"
	body := []byte(
		`{"service":"api","ports":[{"servicePort":53,"protocol":"udp"},{"servicePort":80,"protocol":"tcp"}]}`,
	)
	created, apiError := mirrorRequest(
		handler,
		identity,
		http.MethodPost,
		path,
		body,
		"mirror-1",
	)
	if apiError != nil || created.Code != http.StatusCreated {
		t.Fatalf(
			"create Mirror: status=%d error=%#v body=%s",
			created.Code,
			apiError,
			created.Body.String(),
		)
	}
	var document Document
	if err := json.Unmarshal(created.Body.Bytes(), &document); err != nil ||
		document.ID == "" ||
		document.State != "pending" ||
		len(document.Ports) != 2 ||
		document.Ports[0].ServicePort != 53 {
		t.Fatalf("created Mirror document=%#v err=%v", document, err)
	}
	replayed, apiError := mirrorRequest(
		handler,
		identity,
		http.MethodPost,
		path,
		body,
		"mirror-1",
	)
	if apiError != nil || replayed.Code != http.StatusOK ||
		replayed.Header().Get("Idempotent-Replayed") != "true" ||
		services.calls != 1 {
		t.Fatalf(
			"replay: status=%d error=%#v calls=%d",
			replayed.Code,
			apiError,
			services.calls,
		)
	}
	mismatchBody := []byte(
		`{"service":"other","ports":[{"servicePort":80,"protocol":"tcp"}]}`,
	)
	_, apiError = mirrorRequest(
		handler,
		identity,
		http.MethodPost,
		path,
		mismatchBody,
		"mirror-1",
	)
	if apiError == nil || apiError.Code != controlplaneapi.CodeConflict ||
		services.calls != 1 {
		t.Fatalf(
			"idempotency mismatch error=%#v calls=%d",
			apiError,
			services.calls,
		)
	}
	taskPath := "/api/sessions/" + sessionID + "/mirrors/" + document.ID + "?namespace=development"
	_, apiError = mirrorRequest(
		handler,
		controlplaneapi.Identity{Subject: uuid.NewString(), DeviceID: "other"},
		http.MethodGet,
		taskPath,
		nil,
		"",
	)
	if apiError == nil || apiError.Code != controlplaneapi.CodeNotFound {
		t.Fatalf("cross-identity get error=%#v", apiError)
	}
	stopped, apiError := mirrorRequest(
		handler,
		identity,
		http.MethodDelete,
		taskPath,
		nil,
		"",
	)
	if apiError != nil || stopped.Code != http.StatusOK {
		t.Fatalf(
			"stop pending Mirror: status=%d error=%#v",
			stopped.Code,
			apiError,
		)
	}
	stored, err := stateStore.Tasks().GetByID(ctx, document.ID)
	if err != nil || stored.State != "stopped" {
		t.Fatalf("stored stopped Mirror=%#v err=%v", stored, err)
	}

	second, apiError := mirrorRequest(
		handler,
		identity,
		http.MethodPost,
		path,
		body,
		"mirror-2",
	)
	if apiError != nil {
		t.Fatal(apiError)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.Tasks().
		UpdateState(ctx, document.ID, "pending", "running", json.RawMessage(`{}`), now); err != nil {
		t.Fatal(err)
	}
	taskPath = "/api/sessions/" + sessionID + "/mirrors/" + document.ID + "?namespace=development"
	stopping, apiError := mirrorRequest(
		handler,
		identity,
		http.MethodDelete,
		taskPath,
		nil,
		"",
	)
	if apiError != nil || stopping.Code != http.StatusAccepted {
		t.Fatalf(
			"request running Mirror stop: status=%d error=%#v",
			stopping.Code,
			apiError,
		)
	}
	stored, err = stateStore.Tasks().GetByID(ctx, document.ID)
	if err != nil || stored.State != "stopping" {
		t.Fatalf("stored stopping Mirror=%#v err=%v", stored, err)
	}
}

func newMirrorLifecycleState(
	t *testing.T,
) (context.Context, *storage.Store, time.Time, string, string, sessionapi.ActiveSession) {
	t.Helper()
	ctx := context.Background()
	stateStore, err := storage.Open(ctx, storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "mirror.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stateStore.Close() })
	now := time.Now().UTC()
	identityID, sessionID := uuid.NewString(), uuid.NewString()
	if _, err := stateStore.Identities().Create(ctx, storage.Identity{
		ID: identityID, Type: "human", DisplayName: "Test Identity", Status: "active", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	network, err := networkspec.Normalize(
		networkspec.Spec{ServiceIPs: []string{"10.96.0.10"}},
	)
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
		ID: sessionID, IdentityID: identityID, DeviceID: "device", ClusterID: "cluster",
		Namespace: "development", State: "active", Generation: 1,
		NetworkSpec: networkJSON, NetworkSpecHash: networkHash,
		CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	active := sessionapi.ActiveSession{
		ID:         sessionID,
		Namespace:  "development",
		Generation: 1,
		ExpiresAt:  now.Add(time.Hour),
	}
	return ctx, stateStore, now, identityID, sessionID, active
}

func mirrorRequest(
	handler *Service,
	identity controlplaneapi.Identity,
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
	return response, serveAPI(handler, response, request, identity)
}

func serveAPI(
	handler *Service,
	writer http.ResponseWriter,
	request *http.Request,
	identity controlplaneapi.Identity,
) *controlplaneapi.Error {
	endpoints := NewRoutes(handler).Endpoints()
	parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	request.SetPathValue("sessionID", parts[2])
	if len(parts) > 4 {
		request.SetPathValue("taskID", parts[4])
	}
	switch request.Method {
	case http.MethodPost:
		return endpoints.Create(echo.New().NewContext(request, writer), identity)
	case http.MethodDelete:
		return endpoints.Stop(echo.New().NewContext(request, writer), identity)
	default:
		return endpoints.Get(echo.New().NewContext(request, writer), identity)
	}
}
