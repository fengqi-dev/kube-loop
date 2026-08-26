package remote

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
)

type blockingInventoryConnection struct {
	started chan struct{}
	release chan struct{}
	err     error
	calls   atomic.Int32
}

func (*blockingInventoryConnection) Read(context.Context) (int, []byte, error) {
	return 0, nil, errors.New("unexpected read")
}

func (connection *blockingInventoryConnection) Close(int, string) error {
	connection.calls.Add(1)
	close(connection.started)
	<-connection.release
	return connection.err
}

func TestInventoryWatchAuthenticatesAndValidatesSnapshotBinding(t *testing.T) {
	now := time.Now().UTC()
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/namespaces/development/pods" || request.URL.Query().Get("watch") != "true" ||
			request.Header.Get("Authorization") != "Bearer access-token" {
			http.Error(writer, "invalid request", http.StatusBadRequest)
			return
		}
		connection, err := (&websocket.Upgrader{}).Upgrade(
			writer,
			request, nil)

		if err != nil {
			return
		}
		defer func() { _ = connection.Close() }()
		encoded, _ := json.Marshal(InventorySnapshot{
			SchemaVersion: 1, Type: "snapshot", Resource: InventoryPods, Namespace: "development",
			Sequence: 1, GeneratedAt: now, Pods: []Pod{{Name: "api-0", Namespace: "development"}},
		})
		_ = connection.WriteMessage(websocket.TextMessage, encoded)
		<-request.Context().Done()
	}))
	defer server.Close()
	store := &memoryStore{value: validCredential(now)}
	client, err := New(
		store,
		&fakeRefresher{now: now},
		Config{HTTPClient: server.Client(), Now: func() time.Time { return now }},
	)
	if err != nil {
		t.Fatal(err)
	}
	watch, err := client.OpenInventoryWatch(
		context.Background(),
		profile.Profile{ID: "service-1", BaseURL: server.URL},
		"development",
		InventoryPods,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer checkTestClose(t, watch.Close)
	snapshot, err := watch.Next(context.Background())
	if err != nil || len(snapshot.Pods) != 1 || snapshot.Pods[0].Name != "api-0" {
		t.Fatalf("snapshot = %#v, error = %v", snapshot, err)
	}
}

func TestInventoryWatchConcurrentCloseRetainsError(t *testing.T) {
	closeFailure := errors.New("close Inventory Watch")
	connection := &blockingInventoryConnection{
		started: make(chan struct{}), release: make(chan struct{}), err: closeFailure,
	}
	watch := &InventoryWatch{connection: connection}
	results := make(chan error, 2)
	go func() { results <- watch.Close() }()
	go func() { results <- watch.Close() }()
	<-connection.started
	close(connection.release)
	for range 2 {
		if err := <-results; !errors.Is(err, closeFailure) {
			t.Fatalf("Close() error = %v, want %v", err, closeFailure)
		}
	}
	if calls := connection.calls.Load(); calls != 1 {
		t.Fatalf("connection close calls = %d, want 1", calls)
	}
}
