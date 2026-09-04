package filetransfer

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/protocol/filestream"
	"github.com/fengqi-dev/kube-loop/internal/utils"
)

func TestManagerUploadsLocalFilePersistsProgressAndHistory(t *testing.T) {
	contents := bytes.Repeat([]byte("manager-upload-"), 30_000)
	received := make(chan []byte, 1)
	server := uploadServer(t, func(value []byte) { received <- value })
	defer server.Close()
	root := t.TempDir()
	source := filepath.Join(root, "source.bin")
	if err := os.WriteFile(source, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	events := make(chan Task, 32)
	statePath := filepath.Join(root, "transfers.json")
	manager, err := NewManager(testClient{endpoint: websocketURL(server.URL)}, Config{
		StatePath: statePath, MaximumBytes: 1 << 20, OnEvent: func(task Task) { events <- task },
	})
	if err != nil {
		t.Fatal(err)
	}
	request := Request{
		ProfileID:  "server",
		Direction:  fileTransferDirectionUpload,
		Kind:       fileTransferKindFile,
		Pod:        "api-0",
		Container:  "api",
		LocalPath:  source,
		RemotePath: "/workspace/source.bin",
	}
	task, err := manager.Start(profile.Profile{ID: "server"}, activeFileSession(), request)
	if err != nil {
		t.Fatal(err)
	}
	completed := waitTransferTask(t, events, task.ID)
	checksum := sha256.Sum256(contents)
	if completed.Status != StatusCompleted || completed.DoneBytes != uint64(len(contents)) ||
		completed.TotalBytes != uint64(len(contents)) || completed.Checksum != filestream.FormatChecksum(checksum) {
		t.Fatalf("completed task = %#v", completed)
	}
	select {
	case uploaded := <-received:
		if !bytes.Equal(uploaded, contents) {
			t.Fatalf("uploaded bytes = %d", len(uploaded))
		}
	case <-time.After(time.Second):
		t.Fatal("server did not receive upload")
	}
	if err := manager.Shutdown(); err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewManager(testClient{}, Config{StatePath: statePath, MaximumBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer checkTestClose(t, reloaded.Shutdown)
	history := reloaded.List("server")
	if len(history) != 1 || history[0].ID != task.ID || history[0].Status != StatusCompleted {
		t.Fatalf("reloaded history = %#v", history)
	}
}

func TestManagerListDoesNotBlockOnCheckpointWrite(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.bin")
	if err := os.WriteFile(source, []byte("contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(testClient{}, Config{StatePath: filepath.Join(root, "transfers.json")})
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	var startOnce, releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	manager.writeFile = func(path string, raw []byte, dirMode, fileMode os.FileMode) error {
		startOnce.Do(func() { close(started) })
		<-release
		return utils.WriteFile(path, raw, dirMode, fileMode)
	}
	type startResult struct {
		task Task
		err  error
	}
	result := make(chan startResult, 1)
	go func() {
		task, startErr := manager.Start(
			profile.Profile{ID: "server"},
			activeFileSession(),
			Request{
				ProfileID: "server", Direction: fileTransferDirectionUpload, Kind: fileTransferKindFile,
				Pod: "api-0", LocalPath: source, RemotePath: "/workspace/source.bin",
			},
		)
		result <- startResult{task: task, err: startErr}
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("file transfer checkpoint write did not start")
	}
	listed := make(chan []Task, 1)
	go func() { listed <- manager.List("server") }()
	select {
	case tasks := <-listed:
		if len(tasks) != 0 {
			t.Fatalf("List during checkpoint = %#v", tasks)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("List blocked on file transfer checkpoint write")
	}
	stopped := make(chan error, 1)
	go func() { stopped <- manager.StopProfile("server") }()
	select {
	case err := <-stopped:
		t.Fatalf("StopProfile bypassed an in-flight checkpoint: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(release) })
	startedTask := <-result
	if startedTask.err != nil || startedTask.task.ID == "" {
		t.Fatalf("Start = %#v, %v", startedTask.task, startedTask.err)
	}
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	if err := manager.Shutdown(); err != nil {
		t.Fatal(err)
	}
}

func TestManagerClearHistoryWriteFailureRestoresTasks(t *testing.T) {
	manager, err := NewManager(testClient{}, Config{StatePath: filepath.Join(t.TempDir(), "transfers.json")})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	task := Task{
		ID: uuid.NewString(), ProfileID: "server", Status: StatusCompleted,
		CreatedAt: now, UpdatedAt: now, CompletedAt: &now,
	}
	manager.mu.Lock()
	manager.tasks[task.ID] = task
	manager.mu.Unlock()
	manager.persistMu.Lock()
	if err := manager.persist(cloneTasks(manager.tasks)); err != nil {
		manager.persistMu.Unlock()
		t.Fatal(err)
	}
	manager.persistMu.Unlock()
	writeErr := errors.New("disk unavailable")
	manager.writeFile = func(string, []byte, os.FileMode, os.FileMode) error { return writeErr }
	if err := manager.ClearHistory("server"); !errors.Is(err, writeErr) {
		t.Fatalf("ClearHistory error = %v", err)
	}
	if tasks := manager.List("server"); len(tasks) != 1 || tasks[0].ID != task.ID {
		t.Fatalf("tasks after failed ClearHistory = %#v", tasks)
	}
}

func TestManagerStopsBeforeRemoteTransferWhenPreparingCheckpointFails(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.bin")
	if err := os.WriteFile(source, []byte("contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	events := make(chan Task, 8)
	statePath := filepath.Join(root, "transfers.json")
	manager, err := NewManager(testClient{}, Config{
		StatePath: statePath, MaximumBytes: 1 << 20, OnEvent: func(task Task) { events <- task },
	})
	if err != nil {
		t.Fatal(err)
	}
	writeErr := errors.New("disk unavailable")
	writes := 0
	manager.writeFile = func(path string, raw []byte, dirMode, fileMode os.FileMode) error {
		writes++
		if writes == 1 {
			return utils.WriteFile(path, raw, dirMode, fileMode)
		}
		return writeErr
	}
	task, err := manager.Start(
		profile.Profile{ID: "server"},
		activeFileSession(),
		Request{
			ProfileID: "server", Direction: fileTransferDirectionUpload, Kind: fileTransferKindFile,
			Pod: "api-0", LocalPath: source, RemotePath: "/workspace/source.bin",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	queued := <-events
	failed := <-events
	if queued.ID != task.ID || queued.Status != StatusQueued || failed.Status != StatusFailed ||
		!strings.Contains(failed.Error, "checkpoint file transfer state") {
		t.Fatalf("events = %#v / %#v", queued, failed)
	}
	if err := manager.Shutdown(); !errors.Is(err, writeErr) {
		t.Fatalf("Shutdown() error = %v, want %v", err, writeErr)
	}
	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var persisted persistedState
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatal(err)
	}
	if len(persisted.Tasks) != 1 || persisted.Tasks[0].ID != task.ID ||
		persisted.Tasks[0].Status != StatusQueued {
		t.Fatalf("persisted state = %#v", persisted)
	}
}

func TestManagerMarksTerminalPersistenceFailureAndRetriesOnShutdown(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(root, "transfers.json")
	events := make(chan Task, 2)
	manager, err := NewManager(testClient{}, Config{
		StatePath: statePath, MaximumBytes: 1 << 20, OnEvent: func(task Task) { events <- task },
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	task := Task{
		ID: uuid.NewString(), ProfileID: "server", SessionID: "session", Namespace: "development",
		Direction: fileTransferDirectionUpload, Kind: fileTransferKindFile, Pod: "api-0",
		LocalPath: filepath.Join(root, "source.bin"), RemotePath: "/workspace/source.bin",
		ResumeID: "", Status: StatusRunning, CreatedAt: now, UpdatedAt: now,
	}
	task.ResumeID = task.ID
	manager.mu.Lock()
	manager.tasks[task.ID] = task
	manager.active[task.ID] = &activeTransfer{}
	manager.mu.Unlock()
	manager.persistMu.Lock()
	if err := manager.persist(cloneTasks(manager.tasks)); err != nil {
		manager.persistMu.Unlock()
		t.Fatal(err)
	}
	manager.persistMu.Unlock()
	persistErr := errors.New("disk unavailable")
	manager.writeFile = func(string, []byte, os.FileMode, os.FileMode) error { return persistErr }
	manager.finish(task.ID, filestream.TransferResult{Status: filestream.ResultSucceeded}, nil, false)

	failed := <-events
	if failed.Status != StatusFailed || !strings.Contains(failed.Error, "persist terminal file transfer state") {
		t.Fatalf("terminal event = %#v", failed)
	}
	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var beforeRetry persistedState
	if err := json.Unmarshal(raw, &beforeRetry); err != nil {
		t.Fatal(err)
	}
	if len(beforeRetry.Tasks) != 1 || beforeRetry.Tasks[0].Status != StatusRunning {
		t.Fatalf("state before retry = %#v", beforeRetry)
	}

	manager.writeFile = utils.WriteFile
	if err := manager.Shutdown(); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var afterRetry persistedState
	if err := json.Unmarshal(raw, &afterRetry); err != nil {
		t.Fatal(err)
	}
	if len(afterRetry.Tasks) != 1 || afterRetry.Tasks[0].Status != StatusFailed ||
		!strings.Contains(afterRetry.Tasks[0].Error, "persist terminal file transfer state") {
		t.Fatalf("state after retry = %#v", afterRetry)
	}
}
