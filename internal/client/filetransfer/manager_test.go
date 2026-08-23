package filetransfer

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/fsatomic"
	"github.com/fengqi-dev/kube-loop/internal/protocol/filestream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/websocket"
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
		return fsatomic.WriteFile(path, raw, dirMode, fileMode)
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

func TestManagerUploadsDirectorySnapshotAndCleansTemporaryArchive(t *testing.T) {
	received := make(chan []byte, 1)
	server := uploadServer(t, func(value []byte) { received <- value })
	defer server.Close()
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.MkdirAll(filepath.Join(source, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "nested", "report.txt"), []byte("report"), 0o600); err != nil {
		t.Fatal(err)
	}
	temporaryDir := filepath.Join(root, "temporary")
	events := make(chan Task, 32)
	manager, err := NewManager(testClient{endpoint: websocketURL(server.URL)}, Config{
		TemporaryDir: temporaryDir,
		MaximumBytes: 1 << 20,
		OnEvent:      func(task Task) { events <- task },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer checkTestClose(t, manager.Shutdown)
	task, err := manager.Start(profile.Profile{ID: "server"}, activeFileSession(), Request{
		ProfileID: "server", Direction: fileTransferDirectionUpload, Kind: fileTransferKindDirectory,
		Pod: "api-0", LocalPath: source, RemotePath: "/workspace/source",
	})
	if err != nil {
		t.Fatal(err)
	}
	completed := waitTransferTask(t, events, task.ID)
	if completed.Status != StatusCompleted || completed.TotalBytes == 0 || completed.Checksum == "" {
		t.Fatalf("completed task = %#v", completed)
	}
	select {
	case archive := <-received:
		reader := tar.NewReader(bytes.NewReader(archive))
		found := false
		for {
			header, err := reader.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			contents, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			if header.Name == "nested/report.txt" && string(contents) == "report" {
				found = true
			}
		}
		if !found {
			t.Fatal("uploaded directory archive does not contain nested/report.txt")
		}
	case <-time.After(time.Second):
		t.Fatal("server did not receive directory upload")
	}
	temporary, err := filepath.Glob(filepath.Join(temporaryDir, "kubeloop-upload-*.tar"))
	if err != nil || len(temporary) != 0 {
		t.Fatalf("temporary upload archives = %#v err = %v", temporary, err)
	}
}

func TestManagerDownloadsThroughSameDirectoryTemporaryAndPublishes(t *testing.T) {
	contents := bytes.Repeat([]byte("manager-download-"), 20_000)
	server := downloadServer(t, contents)
	defer server.Close()
	root := t.TempDir()
	destination := filepath.Join(root, "destination.bin")
	events := make(chan Task, 32)
	manager, err := NewManager(testClient{endpoint: websocketURL(server.URL)}, Config{
		MaximumBytes: 1 << 20, OnEvent: func(task Task) { events <- task },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer checkTestClose(t, manager.Shutdown)
	task, err := manager.Start(profile.Profile{ID: "server"}, activeFileSession(), Request{
		ProfileID: "server", Direction: fileTransferDirectionDownload, Kind: fileTransferKindFile, Pod: "api-0",
		LocalPath: destination, RemotePath: "/workspace/destination.bin",
	})
	if err != nil {
		t.Fatal(err)
	}
	completed := waitTransferTask(t, events, task.ID)
	if completed.Status != StatusCompleted {
		t.Fatalf("completed task = %#v", completed)
	}
	downloaded, err := os.ReadFile(destination)
	if err != nil || !bytes.Equal(downloaded, contents) {
		t.Fatalf("downloaded bytes = %d err = %v", len(downloaded), err)
	}
	temporary, _ := filepath.Glob(filepath.Join(root, ".kubeloop-*.part"))
	if len(temporary) != 0 {
		t.Fatalf("temporary downloads remain: %#v", temporary)
	}
}

func TestManagerRejectsUnsafeDownloadedDirectoryWithoutPublishing(t *testing.T) {
	archive := tarBytes(
		t,
		tar.Header{Name: "safe/../escape", Typeflag: tar.TypeReg, Mode: 0o600, Size: 4},
		[]byte("evil"),
	)
	server := downloadServer(t, archive)
	defer server.Close()
	root := t.TempDir()
	destination := filepath.Join(root, fileTransferKindDirectory)
	events := make(chan Task, 32)
	manager, err := NewManager(testClient{endpoint: websocketURL(server.URL)}, Config{
		MaximumBytes: 1 << 20, OnEvent: func(task Task) { events <- task },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer checkTestClose(t, manager.Shutdown)
	task, err := manager.Start(profile.Profile{ID: "server"}, activeFileSession(), Request{
		ProfileID: "server", Direction: fileTransferDirectionDownload, Kind: fileTransferKindDirectory, Pod: "api-0",
		LocalPath: destination, RemotePath: "/workspace/directory",
	})
	if err != nil {
		t.Fatal(err)
	}
	failed := waitTransferTask(t, events, task.ID)
	if failed.Status != StatusFailed || failed.Error == "" {
		t.Fatalf("failed task = %#v", failed)
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Fatalf("unsafe destination was published: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "escape")); !os.IsNotExist(err) {
		t.Fatalf("archive escaped extraction root: %v", err)
	}
}

func TestManagerStopProfileWaitsForStreamAndMarksTaskCancelled(t *testing.T) {
	accepted := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(writer, request, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer checkTestClose(t, connection.CloseNow)
		connection.SetReadLimit(filestream.MaximumData + 1)
		close(accepted)
		for {
			if _, _, err := connection.Read(request.Context()); err != nil {
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
		connection, err := websocket.Accept(writer, request, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer checkTestClose(t, connection.CloseNow)
		connection.SetReadLimit(filestream.MaximumData + 1)
		close(accepted)
		waitForTransferClientClose(request.Context(), connection)
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

func TestDirectorySnapshotAndExtractionPreserveSafeContents(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	nested := filepath.Join(source, "readonly")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	contents := []byte("directory snapshot")
	if err := os.WriteFile(filepath.Join(nested, "data.txt"), contents, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(nested, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(nested, 0o700) })
	archive, err := os.CreateTemp(root, "archive-*.tar")
	if err != nil {
		t.Fatal(err)
	}
	defer checkTestClose(t, archive.Close)
	checksum, size, err := createArchive(context.Background(), source, archive, 1<<20)
	if err != nil || size == 0 || checksum == ([32]byte{}) {
		t.Fatalf("snapshot size = %d checksum = %x err = %v", size, checksum, err)
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "destination")
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := extractArchive(context.Background(), archive, destination, 1<<20); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(destination, "readonly"), 0o700) })
	extracted, err := os.ReadFile(filepath.Join(destination, "readonly", "data.txt"))
	if err != nil || !bytes.Equal(extracted, contents) {
		t.Fatalf("extracted = %q err = %v", extracted, err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(destination, "readonly"))
		if err != nil || info.Mode().Perm() != 0o500 {
			t.Fatalf("directory mode = %v err = %v", info.Mode().Perm(), err)
		}
	}
}

func TestManagerRecoversActivePersistedTaskAsInterrupted(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(root, "transfers.json")
	now := time.Now().UTC()
	contents, err := json.Marshal(persistedState{Version: stateVersion, Tasks: []Task{
		{
			ID:         uuid.NewString(),
			ProfileID:  "server",
			SessionID:  "session",
			Namespace:  "development",
			Direction:  fileTransferDirectionUpload,
			Kind:       fileTransferKindFile,
			Pod:        "api-0",
			LocalPath:  filepath.Join(root, "source.bin"),
			RemotePath: "/workspace/source.bin",
			Status:     StatusRunning,
			CreatedAt:  now,
			UpdatedAt:  now,
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(testClient{}, Config{StatePath: statePath})
	if err != nil {
		t.Fatal(err)
	}
	defer checkTestClose(t, manager.Shutdown)
	items := manager.List("server")
	if len(items) != 1 || items[0].Status != StatusInterrupted || items[0].CompletedAt == nil || items[0].Error == "" {
		t.Fatalf("recovered tasks = %#v", items)
	}
}

func TestManagerDropsInvalidPersistedTaskAndKeepsValidHistory(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(root, "transfers.json")
	now := time.Now().UTC()
	valid := Task{
		ID:         uuid.NewString(),
		ProfileID:  "server",
		SessionID:  "session",
		Namespace:  "development",
		Direction:  fileTransferDirectionUpload,
		Kind:       fileTransferKindFile,
		Pod:        "api-0",
		LocalPath:  filepath.Join(root, "source.bin"),
		RemotePath: "/workspace/source.bin",
		Status:     StatusCompleted,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	valid.ResumeID = valid.ID
	invalid := valid
	invalid.ID = "invalid-task"
	invalid.ResumeID = invalid.ID
	contents, err := json.Marshal(persistedState{Version: stateVersion, Tasks: []Task{invalid, valid}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(testClient{}, Config{StatePath: statePath})
	if err != nil {
		t.Fatal(err)
	}
	defer checkTestClose(t, manager.Shutdown)
	items := manager.List("server")
	if len(items) != 1 || items[0].ID != valid.ID {
		t.Fatalf("recovered tasks = %#v", items)
	}
}

func TestManagerResumesUploadAcrossProcessFromControllerNegotiatedOffset(t *testing.T) {
	root := t.TempDir()
	contents := bytes.Repeat([]byte("resume-upload-"), 40_000)
	offset := uint64(len(contents) / 3)
	checksum := sha256.Sum256(contents)
	taskID := uuid.NewString()
	resumeID := taskID
	receivedTail := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(writer, request, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer checkTestClose(t, connection.CloseNow)
		connection.SetReadLimit(filestream.MaximumData + 1)
		transferred := offset
		var tail []byte
		for {
			_, encoded, err := connection.Read(request.Context())
			if err != nil {
				t.Error(err)
				return
			}
			frame, err := filestream.Decode(encoded)
			if err != nil {
				t.Error(err)
				return
			}
			if frame.Type == filestream.Data {
				tail = append(tail, frame.Payload...)
				transferred += uint64(len(frame.Payload))
				progress, _ := filestream.EncodeProgress(
					filestream.ProgressStatus{Transferred: transferred, Total: uint64(len(contents))},
				)
				_ = connection.Write(request.Context(), websocket.MessageBinary, progress)
				continue
			}
			result, _ := filestream.EncodeResult(filestream.TransferResult{
				Status:      filestream.ResultSucceeded,
				Transferred: uint64(len(contents)),
				Checksum:    checksum,
				HasChecksum: true,
			})
			if err := connection.Write(request.Context(), websocket.MessageBinary, result); err != nil {
				t.Error(err)
				return
			}
			receivedTail <- tail
			waitForTransferClientClose(request.Context(), connection)
			return
		}
	}))
	defer server.Close()
	source := filepath.Join(root, "source.bin")
	if err := os.WriteFile(source, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	statePath := filepath.Join(root, "transfers.json")
	writeTransferState(t, statePath, Task{
		ID:         taskID,
		ProfileID:  "server",
		SessionID:  "old-session",
		Namespace:  "development",
		Direction:  fileTransferDirectionUpload,
		Kind:       fileTransferKindFile,
		Pod:        "api-0",
		Container:  "api",
		LocalPath:  source,
		RemotePath: "/workspace/source.bin",
		Status:     StatusInterrupted,
		TotalBytes: uint64(
			len(contents),
		),
		DoneBytes:   offset,
		Checksum:    filestream.FormatChecksum(checksum),
		ResumeID:    resumeID,
		CreatedAt:   now,
		UpdatedAt:   now,
		CompletedAt: &now,
	})
	events := make(chan Task, 32)
	manager, err := NewManager(
		resumeClient{endpoint: websocketURL(server.URL), uploadOffset: offset},
		Config{
			StatePath: statePath, MaximumBytes: 1 << 20, OnEvent: func(task Task) { events <- task },
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer checkTestClose(t, manager.Shutdown)
	resumed, err := manager.Resume(profile.Profile{ID: "server"}, activeFileSession(), "server", taskID)
	if err != nil || resumed.SessionID != "session" || resumed.Status != StatusQueued {
		t.Fatalf("resumed = %#v err = %v", resumed, err)
	}
	completed := waitTransferTask(t, events, taskID)
	if completed.Status != StatusCompleted || completed.DoneBytes != uint64(len(contents)) {
		t.Fatalf("completed = %#v", completed)
	}
	select {
	case tail := <-receivedTail:
		if !bytes.Equal(tail, contents[offset:]) {
			t.Fatalf("uploaded tail length = %d, want %d", len(tail), len(contents)-int(offset))
		}
	case <-time.After(time.Second):
		t.Fatal("resumed upload did not finish")
	}
}

func TestManagerResumesDownloadAcrossProcessUsingStablePartialFile(t *testing.T) {
	root := t.TempDir()
	contents := bytes.Repeat([]byte("resume-download-"), 30_000)
	offset := len(contents) / 4
	checksum := sha256.Sum256(contents)
	taskID := uuid.NewString()
	destination := filepath.Join(root, "destination.bin")
	temporary := downloadTemporaryPath(destination, taskID)
	if err := os.WriteFile(temporary, contents[:offset], 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(writer, request, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer checkTestClose(t, connection.CloseNow)
		for position := offset; position < len(contents); position += filestream.MaximumData {
			end := min(position+filestream.MaximumData, len(contents))
			data, _ := filestream.Encode(filestream.Frame{Type: filestream.Data, Payload: contents[position:end]})
			_ = connection.Write(request.Context(), websocket.MessageBinary, data)
		}
		result, _ := filestream.EncodeResult(filestream.TransferResult{
			Status:      filestream.ResultSucceeded,
			Transferred: uint64(len(contents)),
			Checksum:    checksum,
			HasChecksum: true,
		})
		if err := connection.Write(request.Context(), websocket.MessageBinary, result); err != nil {
			t.Error(err)
			return
		}
		waitForTransferClientClose(request.Context(), connection)
	}))
	defer server.Close()
	now := time.Now().UTC()
	statePath := filepath.Join(root, "transfers.json")
	writeTransferState(t, statePath, Task{
		ID:            taskID,
		ProfileID:     "server",
		SessionID:     "old-session",
		Namespace:     "development",
		Direction:     fileTransferDirectionDownload,
		Kind:          fileTransferKindFile,
		Pod:           "api-0",
		LocalPath:     destination,
		RemotePath:    "/workspace/destination.bin",
		TemporaryPath: temporary,
		Status:        StatusInterrupted,
		TotalBytes:    uint64(len(contents)),
		DoneBytes:     uint64(offset),
		CreatedAt:     now,
		UpdatedAt:     now,
		CompletedAt:   &now,
	})
	events := make(chan Task, 32)
	manager, err := NewManager(testClient{endpoint: websocketURL(server.URL)}, Config{
		StatePath: statePath, MaximumBytes: 1 << 20, OnEvent: func(task Task) { events <- task },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer checkTestClose(t, manager.Shutdown)
	if _, err := manager.Resume(profile.Profile{ID: "server"}, activeFileSession(), "server", taskID); err != nil {
		t.Fatal(err)
	}
	completed := waitTransferTask(t, events, taskID)
	if completed.Status != StatusCompleted || completed.DoneBytes != uint64(len(contents)) {
		t.Fatalf("completed = %#v", completed)
	}
	downloaded, err := os.ReadFile(destination)
	if err != nil || !bytes.Equal(downloaded, contents) {
		t.Fatalf("downloaded length = %d err = %v", len(downloaded), err)
	}
	if _, err := os.Lstat(temporary); !os.IsNotExist(err) {
		t.Fatalf("partial file remains after publish: %v", err)
	}
}

func TestManagerRejectsRelativeLocalAndUnsafeRemotePathsBeforeQueueing(t *testing.T) {
	manager, err := NewManager(testClient{}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer checkTestClose(t, manager.Shutdown)
	for _, request := range []Request{
		{ProfileID: "server", Direction: fileTransferDirectionUpload, Kind: fileTransferKindFile, Pod: "api-0", LocalPath: "relative", RemotePath: "/workspace/data"},
		{ProfileID: "server", Direction: fileTransferDirectionUpload, Kind: fileTransferKindFile, Pod: "api-0", LocalPath: filepath.Join(t.TempDir(), "data"), RemotePath: "/workspace/../data"},
	} {
		if _, err := manager.Start(profile.Profile{ID: "server"}, activeFileSession(), request); err == nil {
			t.Fatalf("unsafe request was queued: %#v", request)
		}
	}
}

func uploadServer(t *testing.T, receive func([]byte)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(writer, request, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer checkTestClose(t, connection.CloseNow)
		connection.SetReadLimit(filestream.MaximumData + 1)
		var contents []byte
		for {
			_, encoded, err := connection.Read(request.Context())
			if err != nil {
				t.Error(err)
				return
			}
			frame, err := filestream.Decode(encoded)
			if err != nil {
				t.Error(err)
				return
			}
			if frame.Type == filestream.Data {
				contents = append(contents, frame.Payload...)
				progress, _ := filestream.EncodeProgress(filestream.ProgressStatus{Transferred: uint64(len(contents))})
				_ = connection.Write(request.Context(), websocket.MessageBinary, progress)
				continue
			}
			if frame.Type != filestream.Complete {
				t.Errorf("frame type = %d", frame.Type)
				return
			}
			checksum := sha256.Sum256(contents)
			result, _ := filestream.EncodeResult(filestream.TransferResult{
				Status:      filestream.ResultSucceeded,
				Transferred: uint64(len(contents)),
				Checksum:    checksum,
				HasChecksum: true,
			})
			if err := connection.Write(request.Context(), websocket.MessageBinary, result); err != nil {
				t.Error(err)
				return
			}
			receive(contents)
			waitForTransferClientClose(request.Context(), connection)
			return
		}
	}))
}

func downloadServer(t *testing.T, contents []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(writer, request, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer checkTestClose(t, connection.CloseNow)
		for offset := 0; offset < len(contents); offset += filestream.MaximumData {
			end := min(offset+filestream.MaximumData, len(contents))
			data, _ := filestream.Encode(filestream.Frame{Type: filestream.Data, Payload: contents[offset:end]})
			if err := connection.Write(request.Context(), websocket.MessageBinary, data); err != nil {
				t.Error(err)
				return
			}
		}
		checksum := sha256.Sum256(contents)
		result, _ := filestream.EncodeResult(filestream.TransferResult{
			Status:      filestream.ResultSucceeded,
			Transferred: uint64(len(contents)),
			Checksum:    checksum,
			HasChecksum: true,
		})
		if err := connection.Write(request.Context(), websocket.MessageBinary, result); err != nil {
			t.Error(err)
			return
		}
		waitForTransferClientClose(request.Context(), connection)
	}))
}

func waitForTransferClientClose(ctx context.Context, connection *websocket.Conn) {
	for {
		if _, _, err := connection.Read(ctx); err != nil {
			return
		}
	}
}

func waitTransferTask(t *testing.T, events <-chan Task, taskID string) Task {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case task := <-events:
			if task.ID == taskID &&
				(task.Status == StatusCompleted || task.Status == StatusFailed || task.Status == StatusCancelled) {
				return task
			}
		case <-deadline:
			t.Fatal("timed out waiting for file transfer")
			return Task{}
		}
	}
}

func activeFileSession() remote.Session {
	return remote.Session{ID: "session", Namespace: "development", State: fileTransferSessionActive}
}

type resumeClient struct {
	testClient

	uploadOffset uint64
}

func (client resumeClient) CreateFileTransferTask(
	ctx context.Context,
	serverProfile profile.Profile,
	session remote.Session,
	spec remote.FileTransferSpec,
	key string,
) (remote.FileTransferTask, error) {
	task, err := client.testClient.CreateFileTransferTask(ctx, serverProfile, session, spec, key)
	if err == nil && spec.Direction == fileTransferDirectionUpload {
		task.Offset = client.uploadOffset
	}
	return task, err
}

func writeTransferState(t *testing.T, filename string, task Task) {
	t.Helper()
	contents, err := json.Marshal(persistedState{Version: stateVersion, Tasks: []Task{task}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, contents, 0o600); err != nil {
		t.Fatal(err)
	}
}

func tarBytes(t *testing.T, header tar.Header, contents []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	if err := writer.WriteHeader(&header); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(contents); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
