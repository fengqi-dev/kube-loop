package remote

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

func TestPreviewTaskControlLifecycle(t *testing.T) {
	now := time.Now().UTC()
	store := &memoryStore{value: validCredential(now)}
	session := Session{
		ID: uuid.NewString(), Namespace: "development", State: remoteSessionActive, Generation: 1,
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Minute),
	}
	taskID := uuid.NewString()
	var state atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer access-token" ||
			request.URL.Query().Get(remoteParamNamespace) != "development" {
			t.Errorf("headers=%#v query=%s", request.Header, request.URL.RawQuery)
		}
		if request.Method == http.MethodPost {
			if request.Header.Get(remoteHeaderIdempotencyKey) != "preview-key" {
				t.Errorf("Idempotency-Key=%q", request.Header.Get(remoteHeaderIdempotencyKey))
			}
			var spec PreviewSpec
			if err := json.NewDecoder(request.Body).
				Decode(&spec); err != nil || spec.Name != "local-api" || len(spec.Ports) != 2 {
				t.Errorf("Preview spec=%#v err=%v", spec, err)
			}
		}
		if request.Method == http.MethodDelete {
			state.Store(2)
		}
		if request.Method == http.MethodGet && state.Load() == 0 {
			state.Store(1)
		}
		taskState := remotetask.Pending
		clusterIP := ""
		switch state.Load() {
		case 1:
			taskState, clusterIP = remotetask.Running, "10.96.0.42"
		case 2:
			taskState, clusterIP = remotetask.Stopped, "10.96.0.42"
		}
		_ = json.NewEncoder(writer).Encode(PreviewTask{
			ID: taskID, SessionID: session.ID, Namespace: session.Namespace, State: taskState,
			Name: "local-api", ClusterIP: clusterIP,
			Ports: []PreviewPort{
				{Name: "dns", ServicePort: 53, Protocol: remoteProtocolUDP},
				{Name: "http", ServicePort: 80, Protocol: remoteProtocolTCP},
			},
			CreatedAt: now, UpdatedAt: now,
		})
	}))
	defer server.Close()
	client, err := New(
		store,
		&fakeRefresher{now: now},
		Config{HTTPClient: server.Client(), Now: func() time.Time { return now }},
	)
	if err != nil {
		t.Fatal(err)
	}
	serverProfile := profile.Profile{ID: "service-1", BaseURL: server.URL}
	created, err := client.CreatePreview(context.Background(), serverProfile, session, PreviewSpec{
		Name: "local-api",
		Ports: []PreviewPort{
			{ServicePort: 53, Protocol: remoteProtocolUDP},
			{ServicePort: 80, Protocol: remoteProtocolTCP},
		},
	}, "preview-key")
	if err != nil || created.ID != taskID || created.ClusterIP != "" {
		t.Fatalf("created Preview=%#v err=%v", created, err)
	}
	loaded, err := client.GetPreview(context.Background(), serverProfile, session, taskID)
	if err != nil || loaded.State != "running" || loaded.ClusterIP != "10.96.0.42" {
		t.Fatalf("loaded Preview=%#v err=%v", loaded, err)
	}
	stopped, err := client.StopPreview(context.Background(), serverProfile, session, taskID)
	if err != nil || stopped.State != "stopped" {
		t.Fatalf("stopped Preview=%#v err=%v", stopped, err)
	}
}
