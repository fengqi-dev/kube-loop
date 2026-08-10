package previewapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controller"
	"github.com/fengqi-dev/kube-loop/internal/controller/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controller/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/servicebinding"
	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type previewTestSessions struct{ session sessionapi.ActiveSession }

func (sessions previewTestSessions) RequireActive(
	_ context.Context,
	_ controller.Principal,
	namespace, sessionID string,
) (sessionapi.ActiveSession, *controller.APIError) {
	if namespace != sessions.session.Namespace || sessionID != sessions.session.ID {
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
	_ controller.Principal,
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
		ObjectMeta: metav1.ObjectMeta{Name: snapshot.Service, Namespace: snapshot.Namespace},
		Spec:       corev1.ServiceSpec{ClusterIP: "10.96.0.40"},
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

func (resources *recordingPreviewResources) deletes() int {
	resources.mu.Lock()
	defer resources.mu.Unlock()
	return resources.deleteCalls
}

func (resources *recordingPreviewResources) state() (servicebinding.PreviewServiceSnapshot, string, string) {
	resources.mu.Lock()
	defer resources.mu.Unlock()
	return resources.snapshot, resources.createdID, resources.deletedID
}

func TestPreviewTaskIsOwnedIdempotentAndDurablyStopped(t *testing.T) {
	stateStore, principal, active := previewTestStore(t)
	now := time.Now().UTC()
	resources := &recordingPreviewResources{}
	handler, err := New(
		stateStore, previewTestSessions{session: active}, resources,
		Config{GatewayIP: "127.0.0.1", Now: func() time.Time { return now }},
	)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v2/sessions/" + active.ID + "/previews?namespace=development"
	body := []byte(`{"name":"local-api","ports":[{"servicePort":53,"protocol":"udp"},{"name":"http","servicePort":80,"protocol":"tcp"}]}`)
	created, apiError := previewRequest(handler, principal, http.MethodPost, path, body, "preview-1")
	if apiError != nil || created.Code != http.StatusCreated {
		t.Fatalf("create Preview: status=%d error=%#v body=%s", created.Code, apiError, created.Body.String())
	}
	var document Document
	if err := json.Unmarshal(created.Body.Bytes(), &document); err != nil || document.ID == "" ||
		document.State != "pending" || len(document.Ports) != 2 || document.Ports[0].Name != "udp-53" {
		t.Fatalf("created Preview document=%#v err=%v", document, err)
	}
	replayed, apiError := previewRequest(handler, principal, http.MethodPost, path, body, "preview-1")
	if apiError != nil || replayed.Code != http.StatusOK || replayed.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("replayed Preview: status=%d error=%#v", replayed.Code, apiError)
	}
	_, apiError = previewRequest(
		handler, principal, http.MethodPost, path,
		[]byte(`{"name":"other","ports":[{"servicePort":80,"protocol":"tcp"}]}`), "preview-1",
	)
	if apiError == nil || apiError.Code != controller.CodeConflict {
		t.Fatalf("idempotency mismatch error=%#v", apiError)
	}
	taskPath := "/api/v2/sessions/" + active.ID + "/previews/" + document.ID + "?namespace=development"
	_, apiError = previewRequest(
		handler, controller.Principal{Subject: uuid.NewString()}, http.MethodGet, taskPath, nil, "",
	)
	if apiError == nil || apiError.Code != controller.CodeNotFound {
		t.Fatalf("cross-principal get error=%#v", apiError)
	}
	stopped, apiError := previewRequest(handler, principal, http.MethodDelete, taskPath, nil, "")
	if apiError != nil || stopped.Code != http.StatusOK {
		t.Fatalf("stop pending Preview: status=%d error=%#v", stopped.Code, apiError)
	}
	stored, err := stateStore.Tasks().GetByID(context.Background(), document.ID)
	if err != nil || stored.State != "stopped" {
		t.Fatalf("stored Preview=%#v err=%v", stored, err)
	}
}

func TestPreviewRequestValidationRejectsInvalidKubernetesNames(t *testing.T) {
	stateStore, principal, active := previewTestStore(t)
	handler, err := New(
		stateStore, previewTestSessions{session: active}, &recordingPreviewResources{},
		Config{GatewayIP: "127.0.0.1"},
	)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v2/sessions/" + active.ID + "/previews?namespace=development"
	for _, body := range [][]byte{
		[]byte(`{"name":"Existing_Name","ports":[{"servicePort":80,"protocol":"tcp"}]}`),
		[]byte(`{"name":"local-api","ports":[{"name":"bad_name","servicePort":80,"protocol":"tcp"}]}`),
		[]byte(`{"name":"local-api","ports":[{"name":"http","servicePort":80,"protocol":"tcp"},{"name":"http","servicePort":81,"protocol":"tcp"}]}`),
	} {
		_, apiError := previewRequest(handler, principal, http.MethodPost, path, body, uuid.NewString())
		if apiError == nil || apiError.Code != controller.CodeInvalidArgument {
			t.Fatalf("invalid Preview body=%s error=%#v", body, apiError)
		}
	}
}

func previewTestStore(t *testing.T) (*storage.Store, controller.Principal, sessionapi.ActiveSession) {
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
	principalID, sessionID := uuid.NewString(), uuid.NewString()
	if _, err := stateStore.Principals().Upsert(ctx, storage.Principal{
		ID: principalID, Provider: "test", ExternalID: "preview-user", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	network, err := networkspec.Normalize(networkspec.Spec{ServiceIPs: []string{"10.96.0.10"}})
	if err != nil {
		t.Fatal(err)
	}
	networkJSON, _ := networkspec.CanonicalJSON(network)
	networkHash, _ := networkspec.Hash(network)
	expiresAt := now.Add(time.Hour)
	if err := stateStore.Sessions().Create(ctx, storage.Session{
		ID: sessionID, PrincipalID: principalID, DeviceID: "device", ClusterID: "cluster",
		Namespace: "development", State: "active", Generation: 1,
		NetworkSpec: networkJSON, NetworkSpecHash: networkHash,
		CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: expiresAt,
	}); err != nil {
		t.Fatal(err)
	}
	return stateStore,
		controller.Principal{Subject: principalID, DeviceID: "device"},
		sessionapi.ActiveSession{ID: sessionID, Namespace: "development", Generation: 1, ExpiresAt: expiresAt}
}

func previewRequest(
	handler *Handler,
	principal controller.Principal,
	method, path string,
	body []byte,
	idempotency string,
) (*httptest.ResponseRecorder, *controller.APIError) {
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if idempotency != "" {
		request.Header.Set(sessionapi.IdempotencyHeader, idempotency)
	}
	response := httptest.NewRecorder()
	return response, handler.ServeAPI(response, request, principal)
}
