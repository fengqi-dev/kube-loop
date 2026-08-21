package filetransfer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/protocol/filestream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/websocket"
)

type testClient struct{ endpoint string }

func (client testClient) CreateFileTransferTask(
	_ context.Context,
	_ profile.Profile,
	session remote.Session,
	spec remote.FileTransferSpec,
	_ string,
) (remote.FileTransferTask, error) {
	now := time.Now().UTC()
	return remote.FileTransferTask{
		ID:         uuid.NewString(),
		SessionID:  session.ID,
		Namespace:  session.Namespace,
		State:      "pending",
		Direction:  spec.Direction,
		Kind:       spec.Kind,
		Pod:        spec.Pod,
		Container:  spec.Container,
		RemotePath: spec.RemotePath,
		Size:       spec.Size,
		Offset:     spec.Offset,
		Checksum:   spec.Checksum,
		Overwrite:  spec.Overwrite,
		ResumeID:   spec.ResumeID,
		CreatedAt:  now,
		UpdatedAt:  now,
		ExpiresAt:  now.Add(time.Minute),
	}, nil
}

func (client testClient) OpenFileTransferStream(
	ctx context.Context,
	_ profile.Profile,
	_ remote.Session,
	_ remote.FileTransferTask,
) (*websocket.Conn, error) {
	connection, _, err := websocket.Dial(ctx, client.endpoint, nil)
	return connection, err
}

func TestUploadStreamsDataWhileReceivingProgressAndVerifiesResult(t *testing.T) {
	contents := bytes.Repeat([]byte("upload-data-"), 40_000)
	checksum := sha256.Sum256(contents)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(writer, request, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer checkTestClose(t, connection.CloseNow)
		connection.SetReadLimit(filestream.MaximumData + 1)
		hash := sha256.New()
		var transferred uint64
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
			if frame.Type == filestream.Complete {
				var digest [32]byte
				copy(digest[:], hash.Sum(nil))
				result, _ := filestream.EncodeResult(filestream.TransferResult{
					Status: filestream.ResultSucceeded, Transferred: transferred, Checksum: digest, HasChecksum: true,
				})
				_ = connection.Write(request.Context(), websocket.MessageBinary, result)
				return
			}
			if frame.Type != filestream.Data {
				t.Errorf("frame type = %d", frame.Type)
				return
			}
			_, _ = hash.Write(frame.Payload)
			transferred += uint64(len(frame.Payload))
			progress, _ := filestream.EncodeProgress(
				filestream.ProgressStatus{Transferred: transferred, Total: uint64(len(contents))},
			)
			if err := connection.Write(request.Context(), websocket.MessageBinary, progress); err != nil {
				t.Error(err)
				return
			}
		}
	}))
	defer server.Close()
	var progress filestream.ProgressStatus
	task, result, err := Upload(
		context.Background(), testClient{endpoint: websocketURL(server.URL)}, profile.Profile{ID: "server"},
		remote.Session{ID: "session", Namespace: "development", State: fileTransferSessionActive},
		remote.FileTransferSpec{
			Direction:  fileTransferDirectionUpload,
			Kind:       fileTransferKindFile,
			Pod:        "api-0",
			RemotePath: "/workspace/data.bin",
			Size:       uint64(len(contents)),
			Checksum:   filestream.FormatChecksum(checksum),
		}, bytes.NewReader(contents), func(value filestream.ProgressStatus) { progress = value },
	)
	if err != nil || task.ID == "" || result.Status != filestream.ResultSucceeded || result.Checksum != checksum ||
		progress.Transferred != uint64(len(contents)) {
		t.Fatalf("task = %#v result = %#v progress = %#v err = %v", task, result, progress, err)
	}
}

func TestDownloadWritesOnlyDataAndVerifiesChecksum(t *testing.T) {
	contents := bytes.Repeat([]byte("download-data-"), 30_000)
	checksum := sha256.Sum256(contents)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(writer, request, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer checkTestClose(t, connection.CloseNow)
		for offset := 0; offset < len(contents); offset += filestream.MaximumData {
			end := min(offset+filestream.MaximumData, len(contents))
			data, _ := filestream.Encode(filestream.Frame{Type: filestream.Data, Payload: contents[offset:end]})
			_ = connection.Write(request.Context(), websocket.MessageBinary, data)
		}
		progress, _ := filestream.EncodeProgress(
			filestream.ProgressStatus{Transferred: uint64(len(contents)), Total: uint64(len(contents))},
		)
		result, _ := filestream.EncodeResult(filestream.TransferResult{
			Status:      filestream.ResultSucceeded,
			Transferred: uint64(len(contents)),
			Checksum:    checksum,
			HasChecksum: true,
		})
		_ = connection.Write(request.Context(), websocket.MessageBinary, progress)
		_ = connection.Write(request.Context(), websocket.MessageBinary, result)
	}))
	defer server.Close()
	var output bytes.Buffer
	var progress filestream.ProgressStatus
	_, result, err := Download(
		context.Background(),
		testClient{endpoint: websocketURL(server.URL)},
		profile.Profile{ID: "server"},
		remote.Session{ID: "session", Namespace: "development", State: fileTransferSessionActive},
		remote.FileTransferSpec{
			Direction:  fileTransferDirectionDownload,
			Kind:       fileTransferKindFile,
			Pod:        "api-0",
			RemotePath: "/workspace/data.bin",
		},
		&output,
		func(value filestream.ProgressStatus) { progress = value },
	)
	if err != nil || !bytes.Equal(output.Bytes(), contents) || result.Checksum != checksum ||
		progress.Total != uint64(len(contents)) {
		t.Fatalf("output bytes = %d result = %#v progress = %#v err = %v", output.Len(), result, progress, err)
	}
}

func TestDownloadCancelsGatewayWhenLocalWriterFails(t *testing.T) {
	cancelled := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(writer, request, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer checkTestClose(t, connection.CloseNow)
		data, _ := filestream.Encode(filestream.Frame{Type: filestream.Data, Payload: []byte("payload")})
		if err := connection.Write(request.Context(), websocket.MessageBinary, data); err != nil {
			t.Error(err)
			return
		}
		_, encoded, err := connection.Read(request.Context())
		if err != nil {
			t.Error(err)
			return
		}
		frame, err := filestream.Decode(encoded)
		if err == nil && frame.Type == filestream.Cancel {
			cancelled <- struct{}{}
		}
	}))
	defer server.Close()
	_, _, err := Download(
		context.Background(),
		testClient{endpoint: websocketURL(server.URL)},
		profile.Profile{ID: "server"},
		remote.Session{ID: "session", Namespace: "development", State: fileTransferSessionActive},
		remote.FileTransferSpec{
			Direction:  fileTransferDirectionDownload,
			Kind:       fileTransferKindFile,
			Pod:        "api-0",
			RemotePath: "/workspace/data.bin",
		},
		failingWriter{},
		nil,
	)
	if err == nil {
		t.Fatal("local writer failure was ignored")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("Gateway did not receive cancel frame")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func websocketURL(serverURL string) string { return "ws" + strings.TrimPrefix(serverURL, "http") }

var _ io.Writer = failingWriter{}
