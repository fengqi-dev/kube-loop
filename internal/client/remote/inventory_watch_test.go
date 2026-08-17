package remote

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/protocol/websocket"
)

func TestInventoryWatchAuthenticatesAndValidatesSnapshotBinding(t *testing.T) {
	now := time.Now().UTC()
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/namespaces/development/pods" || request.URL.Query().Get("watch") != "true" ||
			request.Header.Get("Authorization") != "Bearer access-token" {
			http.Error(writer, "invalid request", http.StatusBadRequest)
			return
		}
		connection, err := websocket.Accept(writer, request, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
		if err != nil {
			return
		}
		defer connection.CloseNow()
		encoded, _ := json.Marshal(InventorySnapshot{
			SchemaVersion: 1, Type: "snapshot", Resource: InventoryPods, Namespace: "development",
			Sequence: 1, GeneratedAt: now, Pods: []Pod{{Name: "api-0", Namespace: "development"}},
		})
		_ = connection.Write(request.Context(), websocket.MessageText, encoded)
		<-request.Context().Done()
	}))
	defer server.Close()
	store := &memoryStore{value: validCredential(now)}
	client, err := New(store, &fakeRefresher{now: now}, Config{HTTPClient: server.Client(), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	watch, err := client.OpenInventoryWatch(context.Background(), profile.Profile{ID: "service-1", BaseURL: server.URL}, "development", InventoryPods)
	if err != nil {
		t.Fatal(err)
	}
	defer watch.Close()
	snapshot, err := watch.Next(context.Background())
	if err != nil || len(snapshot.Pods) != 1 || snapshot.Pods[0].Name != "api-0" {
		t.Fatalf("snapshot = %#v, error = %v", snapshot, err)
	}
}
