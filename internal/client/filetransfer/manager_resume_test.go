package filetransfer

import (
	"bytes"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/protocol/filestream"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

func TestManagerResumesUploadAcrossProcessFromControllerNegotiatedOffset(t *testing.T) {
	root := t.TempDir()
	contents := bytes.Repeat([]byte("resume-upload-"), 40_000)
	offset := uint64(len(contents) / 3)
	checksum := sha256.Sum256(contents)
	taskID := uuid.NewString()
	resumeID := taskID
	receivedTail := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := (&websocket.Upgrader{}).Upgrade(writer, request, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer checkTestClose(t, connection.Close)
		connection.SetReadLimit(filestream.MaximumData + 1)
		transferred := offset
		var tail []byte
		for {
			_, encoded, err := connection.ReadMessage()
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
				_ = connection.WriteMessage(websocket.BinaryMessage, progress)
				continue
			}
			result, _ := filestream.EncodeResult(filestream.TransferResult{
				Status:      filestream.ResultSucceeded,
				Transferred: uint64(len(contents)),
				Checksum:    checksum,
				HasChecksum: true,
			})
			if err := connection.WriteMessage(websocket.BinaryMessage, result); err != nil {
				t.Error(err)
				return
			}
			receivedTail <- tail
			waitForTransferClientClose(connection)
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
		connection, err := (&websocket.Upgrader{}).Upgrade(writer, request, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer checkTestClose(t, connection.Close)
		for position := offset; position < len(contents); position += filestream.MaximumData {
			end := min(position+filestream.MaximumData, len(contents))
			data, _ := filestream.Encode(filestream.Frame{Type: filestream.Data, Payload: contents[position:end]})
			_ = connection.WriteMessage(websocket.BinaryMessage, data)
		}
		result, _ := filestream.EncodeResult(filestream.TransferResult{
			Status:      filestream.ResultSucceeded,
			Transferred: uint64(len(contents)),
			Checksum:    checksum,
			HasChecksum: true,
		})
		if err := connection.WriteMessage(websocket.BinaryMessage, result); err != nil {
			t.Error(err)
			return
		}
		waitForTransferClientClose(connection)
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
