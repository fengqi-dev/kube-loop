package filetransfer

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/protocol/filestream"
	"github.com/gorilla/websocket"
)

func uploadServer(t *testing.T, receive func([]byte)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := (&websocket.Upgrader{}).Upgrade(writer, request, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer checkTestClose(t, connection.Close)
		connection.SetReadLimit(filestream.MaximumData + 1)
		var contents []byte
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
				contents = append(contents, frame.Payload...)
				progress, _ := filestream.EncodeProgress(filestream.ProgressStatus{Transferred: uint64(len(contents))})
				_ = connection.WriteMessage(websocket.BinaryMessage, progress)
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
			if err := connection.WriteMessage(websocket.BinaryMessage, result); err != nil {
				t.Error(err)
				return
			}
			receive(contents)
			waitForTransferClientClose(connection)
			return
		}
	}))
}

func downloadServer(t *testing.T, contents []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := (&websocket.Upgrader{}).Upgrade(writer, request, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer checkTestClose(t, connection.Close)
		for offset := 0; offset < len(contents); offset += filestream.MaximumData {
			end := min(offset+filestream.MaximumData, len(contents))
			data, _ := filestream.Encode(filestream.Frame{Type: filestream.Data, Payload: contents[offset:end]})
			if err := connection.WriteMessage(websocket.BinaryMessage, data); err != nil {
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
		if err := connection.WriteMessage(websocket.BinaryMessage, result); err != nil {
			t.Error(err)
			return
		}
		waitForTransferClientClose(connection)
	}))
}

func waitForTransferClientClose(connection *websocket.Conn) {
	for {
		if _, _, err := connection.ReadMessage(); err != nil {
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
