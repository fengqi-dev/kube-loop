package previewapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
	corev1 "k8s.io/api/core/v1"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/servicebinding"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
)

type previewTestSessions struct{ session sessionapi.ActiveSession }

func (sessions previewTestSessions) RequireActive(
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

type recordingPreviewResources struct {
	mu          sync.Mutex
	snapshot    servicebinding.PreviewServiceSnapshot
	createdID   string
	deletedID   string
	deleteCalls int
	createErr   error
	deleteErr   error
}

func (resources *recordingPreviewResources) Create(
	_ context.Context,
	_ controlplaneapi.Identity,
	snapshot servicebinding.PreviewServiceSnapshot,
	previewID string,
) (*corev1.Service, error) {
	resources.mu.Lock()
	defer resources.mu.Unlock()
	resources.snapshot = snapshot
	resources.createdID = previewID
	if resources.createErr != nil {
		return nil, resources.createErr
	}
	return &corev1.Service{
		Name:      snapshot.Service,
		Namespace: snapshot.Namespace,
		Spec:      corev1.ServiceSpec{ClusterIP: "10.96.0.40"},
	}, nil
}

func (resources *recordingPreviewResources) Delete(
	_ context.Context,
	snapshot servicebinding.PreviewServiceSnapshot,
	previewID string,
) error {
	resources.mu.Lock()
	defer resources.mu.Unlock()
	resources.snapshot = snapshot
	resources.deletedID = previewID
	resources.deleteCalls++
	return resources.deleteErr
}

func TestPreviewTaskIsOwnedIdempotentAndDurablyStopped(t *testing.T) {
	stateStore, identity, active := previewTestStore(t)
	now := time.Now().UTC()
	resources := &recordingPreviewResources{}
	handler, err := New(
		stateStore, previewTestSessions{session: active}, resources,
		Config{Now: func() time.Time { return now }},
	)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/sessions/" + active.ID + "/previews?namespace=development"
	body := []byte(
		`{"name":"local-api","ports":[{"servicePort":53,"protocol":"udp"},{"name":"http","servicePort":80,"protocol":"tcp"}]}`,
	)
	created, apiError := previewRequest(
		handler,
		identity,
		http.MethodPost,
		path,
		body,
		"preview-1",
	)
	if apiError != nil || created.Code != http.StatusCreated {
		t.Fatalf(
			"create Preview: status=%d error=%#v body=%s",
			created.Code,
			apiError,
			created.Body.String(),
		)
	}
	var document Document
	if err := json.Unmarshal(created.Body.Bytes(), &document); err != nil || document.ID == "" ||
		document.State != "pending" || len(document.Ports) != 2 ||
		document.Ports[0].Name != "udp-53" {
		t.Fatalf("created Preview document=%#v err=%v", document, err)
	}
	if bytes.Contains(created.Body.Bytes(), []byte(`"expiresAt"`)) {
		t.Fatalf(
			"Preview response contains an expiration: %s",
			created.Body.String(),
		)
	}
	stored, err := stateStore.Tasks().GetByID(context.Background(), document.ID)
	if err != nil || stored.ExpiresAt != nil {
		t.Fatalf("stored Preview expiration=%v err=%v", stored.ExpiresAt, err)
	}
	replayed, apiError := previewRequest(
		handler,
		identity,
		http.MethodPost,
		path,
		body,
		"preview-1",
	)
	if apiError != nil || replayed.Code != http.StatusOK ||
		replayed.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf(
			"replayed Preview: status=%d error=%#v",
			replayed.Code,
			apiError,
		)
	}
	_, apiError = previewRequest(
		handler,
		identity,
		http.MethodPost,
		path,
		[]byte(
			`{"name":"other","ports":[{"servicePort":80,"protocol":"tcp"}]}`,
		),
		"preview-1",
	)
	if apiError == nil || apiError.Code != controlplaneapi.CodeConflict {
		t.Fatalf("idempotency mismatch error=%#v", apiError)
	}
	taskPath := "/api/sessions/" + active.ID + "/previews/" + document.ID + "?namespace=development"
	_, apiError = previewRequest(
		handler,
		controlplaneapi.Identity{Subject: uuid.NewString()},
		http.MethodGet,
		taskPath,
		nil,
		"",
	)
	if apiError == nil || apiError.Code != controlplaneapi.CodeNotFound {
		t.Fatalf("cross-identity get error=%#v", apiError)
	}
	stopped, apiError := previewRequest(
		handler,
		identity,
		http.MethodDelete,
		taskPath,
		nil,
		"",
	)
	if apiError != nil || stopped.Code != http.StatusOK {
		t.Fatalf(
			"stop pending Preview: status=%d error=%#v",
			stopped.Code,
			apiError,
		)
	}
	stored, err = stateStore.Tasks().GetByID(context.Background(), document.ID)
	if err != nil || stored.State != "stopped" {
		t.Fatalf("stored Preview=%#v err=%v", stored, err)
	}
}

func TestPreviewRequestValidationRejectsInvalidKubernetesNames(t *testing.T) {
	stateStore, identity, active := previewTestStore(t)
	handler, err := New(
		stateStore,
		previewTestSessions{session: active},
		&recordingPreviewResources{},
		Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/sessions/" + active.ID + "/previews?namespace=development"
	for _, body := range [][]byte{
		[]byte(`{"name":"Existing_Name","ports":[{"servicePort":80,"protocol":"tcp"}]}`),
		[]byte(`{"name":"local-api","ports":[{"name":"bad_name","servicePort":80,"protocol":"tcp"}]}`),
		[]byte(`{"name":"local-api","ports":[{"name":"http","servicePort":80,"protocol":"tcp"},{"name":"http","servicePort":81,"protocol":"tcp"}]}`),
	} {
		_, apiError := previewRequest(
			handler,
			identity,
			http.MethodPost,
			path,
			body,
			uuid.NewString(),
		)
		if apiError == nil ||
			apiError.Code != controlplaneapi.CodeInvalidArgument {
			t.Fatalf("invalid Preview body=%s error=%#v", body, apiError)
		}
	}
}

func previewTestStore(
	t *testing.T,
) (*storage.Store, controlplaneapi.Identity, sessionapi.ActiveSession) {
	t.Helper()
	ctx := context.Background()
	stateStore, err := storage.Open(ctx, storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "preview.db"),
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
	networkJSON, _ := networkspec.CanonicalJSON(network)
	networkHash, _ := networkspec.Hash(network)
	expiresAt := now.Add(time.Hour)
	if err := stateStore.Sessions().Create(ctx, storage.Session{
		ID: sessionID, IdentityID: identityID, DeviceID: "device", ClusterID: "cluster",
		Namespace: "development", State: "active", Generation: 1,
		NetworkSpec: networkJSON, NetworkSpecHash: networkHash,
		CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: expiresAt,
	}); err != nil {
		t.Fatal(err)
	}
	return stateStore,
		controlplaneapi.Identity{Subject: identityID, DeviceID: "device"},
		sessionapi.ActiveSession{
			ID:         sessionID,
			Namespace:  "development",
			Generation: 1,
			ExpiresAt:  expiresAt,
		}
}

func previewRequest(
	handler *Service,
	identity controlplaneapi.Identity,
	method, path string,
	body []byte,
	idempotency string,
) (*httptest.ResponseRecorder, *controlplaneapi.Error) {
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if idempotency != "" {
		request.Header.Set(sessionapi.IdempotencyHeader, idempotency)
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
