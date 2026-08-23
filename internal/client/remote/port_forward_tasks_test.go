package remote

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

func TestPortForwardTaskLifecycleUsesSessionBoundGatewayAPI(t *testing.T) {
	now := time.Now().UTC()
	store := &memoryStore{value: validCredential(now)}
	session := Session{
		ID: uuid.NewString(), Namespace: "development", State: remoteSessionActive, Generation: 1,
		CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: now.Add(time.Minute),
	}
	taskID := uuid.NewString()
	state := remotetask.Running
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.URL.Query().Get(remoteParamNamespace) != session.Namespace ||
			!strings.HasPrefix(request.URL.Path, "/api/sessions/"+session.ID+"/port-forwards") {
			t.Errorf("request = %s %s?%s", request.Method, request.URL.Path, request.URL.RawQuery)
		}
		if request.Method == http.MethodPost {
			if request.Header.Get(remoteHeaderIdempotencyKey) != "pf-key" ||
				request.Header.Get("Content-Type") != "application/json" {
				t.Errorf("headers = %#v", request.Header)
			}
			var spec PortForwardSpec
			if err := json.NewDecoder(request.Body).
				Decode(&spec); err != nil || spec.Name != "api" ||
				spec.RemotePort != 8443 {
				t.Errorf("spec = %#v err = %v", spec, err)
			}
		}
		if request.Method == http.MethodDelete {
			state = remotetask.Stopped
		}
		document := PortForwardTask{
			ID: taskID, SessionID: session.ID, Namespace: session.Namespace, State: state,
			Kind: remoteResourceService, Name: "api", Protocol: remoteProtocolTCP, RemotePort: 8443,
			DialAddress: "10.96.0.20:8443", CreatedAt: now, UpdatedAt: now, ExpiresAt: session.ExpiresAt,
		}
		if request.Method == http.MethodGet {
			_ = json.NewEncoder(writer).Encode(struct {
				Items []PortForwardTask `json:"items"`
			}{Items: []PortForwardTask{document}})
			return
		}
		_ = json.NewEncoder(writer).Encode(document)
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
	created, err := client.CreatePortForward(context.Background(), serverProfile, session, PortForwardSpec{
		Kind: remoteResourceService, Name: "api", Protocol: remoteProtocolTCP, RemotePort: 8443,
	}, "pf-key")
	if err != nil || created.DialAddress != "10.96.0.20:8443" {
		t.Fatalf("created = %#v err = %v", created, err)
	}
	listed, err := client.ListPortForwards(context.Background(), serverProfile, session)
	if err != nil || len(listed) != 1 || listed[0].ID != taskID {
		t.Fatalf("listed = %#v err = %v", listed, err)
	}
	stopped, err := client.StopPortForward(context.Background(), serverProfile, session, taskID)
	if err != nil || stopped.State != "stopped" || calls.Load() != 3 {
		t.Fatalf("stopped = %#v calls = %d err = %v", stopped, calls.Load(), err)
	}
}
