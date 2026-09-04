package filetransfer

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/protocol/filestream"
	"github.com/gorilla/websocket"
)

func TestManagerStopProfileWaitsForStreamAndMarksTaskCancelled(t *testing.T) {
	accepted := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := (&websocket.Upgrader{}).Upgrade(writer, request, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer checkTestClose(t, connection.Close)
		connection.SetReadLimit(filestream.MaximumData + 1)
		close(accepted)
		for {
			if _, _, err := connection.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	root := t.TempDir()
	source := filepath.Join(root, "source.bin")
	if err := os.WriteFile(source, []byte("contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	events := make(chan Task, 32)
	manager, err := NewManager(testClient{endpoint: websocketURL(server.URL)}, Config{
		MaximumBytes: 1 << 20, OnEvent: func(task Task) { events <- task },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer checkTestClose(t, manager.Shutdown)
	task, err := manager.Start(profile.Profile{ID: "server"}, activeFileSession(), Request{
		ProfileID: "server", Direction: fileTransferDirectionUpload, Kind: fileTransferKindFile, Pod: "api-0",
		LocalPath: source, RemotePath: "/workspace/source.bin",
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("upload stream was not opened")
	}
	if err := manager.StopProfile("server"); err != nil {
		t.Fatal(err)
	}
	items := manager.List("server")
	if len(items) != 1 || items[0].ID != task.ID || items[0].Status != StatusCancelled {
		t.Fatalf("tasks after stop = %#v", items)
	}
}

func TestManagerCancelAndClearHistoryPersistLifecycle(t *testing.T) {
	accepted := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := (&websocket.Upgrader{}).Upgrade(writer, request, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer checkTestClose(t, connection.Close)
		connection.SetReadLimit(filestream.MaximumData + 1)
		close(accepted)
		waitForTransferClientClose(connection)
	}))
	defer server.Close()
	root := t.TempDir()
	source := filepath.Join(root, "source.bin")
	if err := os.WriteFile(source, []byte("contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(root, "transfers.json")
	events := make(chan Task, 32)
	manager, err := NewManager(testClient{endpoint: websocketURL(server.URL)}, Config{
		StatePath: statePath, MaximumBytes: 1 << 20, OnEvent: func(task Task) { events <- task },
	})
	if err != nil {
		t.Fatal(err)
	}
	task, err := manager.Start(profile.Profile{ID: "server"}, activeFileSession(), Request{
		ProfileID: "server", Direction: fileTransferDirectionUpload, Kind: fileTransferKindFile,
		Pod: "api-0", LocalPath: source, RemotePath: "/workspace/source.bin",
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("upload stream was not opened")
	}
	if err := manager.Cancel("server", task.ID); err != nil {
		t.Fatal(err)
	}
	cancelled := waitTransferTask(t, events, task.ID)
	if cancelled.Status != StatusCancelled || cancelled.Error != "" {
		t.Fatalf("cancelled task = %#v", cancelled)
	}
	if err := manager.Cancel("server", task.ID); err == nil {
		t.Fatal("completed transfer was accepted for cancellation")
	}
	if err := manager.ClearHistory("server"); err != nil {
		t.Fatal(err)
	}
	if tasks := manager.List("server"); len(tasks) != 0 {
		t.Fatalf("tasks after history clear = %#v", tasks)
	}
	if err := manager.Shutdown(); err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewManager(testClient{}, Config{StatePath: statePath, MaximumBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer checkTestClose(t, reloaded.Shutdown)
	if tasks := reloaded.List("server"); len(tasks) != 0 {
		t.Fatalf("reloaded tasks after history clear = %#v", tasks)
	}
}
