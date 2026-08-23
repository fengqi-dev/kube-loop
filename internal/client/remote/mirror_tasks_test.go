package remote

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

func TestMirrorTaskControlLifecycle(t *testing.T) {
	now := time.Now().UTC()
	store := &memoryStore{value: validCredential(now)}
	session := Session{
		ID: uuid.NewString(), Namespace: "development", State: remoteSessionActive, Generation: 1,
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Minute),
	}
	taskID := uuid.NewString()
	state := remotetask.Pending
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer access-token" ||
			request.URL.Query().Get(remoteParamNamespace) != "development" {
			t.Errorf("headers=%#v query=%s", request.Header, request.URL.RawQuery)
		}
		if request.Method == http.MethodPost {
			if request.Header.Get(remoteHeaderIdempotencyKey) != "mirror-key" {
				t.Errorf("Idempotency-Key=%q", request.Header.Get(remoteHeaderIdempotencyKey))
			}
			var spec MirrorSpec
			if err := json.NewDecoder(request.Body).
				Decode(&spec); err != nil || spec.Service != "api" || len(spec.Ports) != 2 {
				t.Errorf("Mirror spec=%#v err=%v", spec, err)
			}
		}
		if request.Method == http.MethodDelete {
			state = remotetask.Stopped
		}
		_ = json.NewEncoder(writer).Encode(MirrorTask{
			ID: taskID, SessionID: session.ID, Namespace: session.Namespace, State: state,
			Service: "api", ClusterIP: "10.96.0.20",
			Ports: []MirrorPort{
				{Name: "dns", ServicePort: 53, Protocol: remoteProtocolUDP},
				{Name: "http", ServicePort: 80, Protocol: remoteProtocolTCP},
			},
			CreatedAt: now, UpdatedAt: now, ExpiresAt: session.ExpiresAt,
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
	created, err := client.CreateMirror(context.Background(), serverProfile, session, MirrorSpec{
		Service: "api",
		Ports: []MirrorPort{
			{ServicePort: 53, Protocol: remoteProtocolUDP},
			{ServicePort: 80, Protocol: remoteProtocolTCP},
		},
	}, "mirror-key")
	if err != nil || created.ID != taskID {
		t.Fatalf("created Mirror=%#v err=%v", created, err)
	}
	loaded, err := client.GetMirror(context.Background(), serverProfile, session, taskID)
	if err != nil || loaded.ClusterIP != "10.96.0.20" {
		t.Fatalf("loaded Mirror=%#v err=%v", loaded, err)
	}
	stopped, err := client.StopMirror(context.Background(), serverProfile, session, taskID)
	if err != nil || stopped.State != "stopped" {
		t.Fatalf("stopped Mirror=%#v err=%v", stopped, err)
	}
}
