package filetransfer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/protocol/filestream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/websocket"
)

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
			Status: filestream.ResultSucceeded, Transferred: uint64(len(contents)),
			Checksum: checksum, HasChecksum: true,
		})
		_ = connection.Write(request.Context(), websocket.MessageBinary, progress)
		_ = connection.Write(request.Context(), websocket.MessageBinary, result)
	}))
	defer server.Close()
	var output bytes.Buffer
	var progress filestream.ProgressStatus
	_, result, err := Download(
		context.Background(), testClient{endpoint: websocketURL(server.URL)}, profile.Profile{ID: "server"},
		remote.Session{ID: "session", Namespace: "development", State: fileTransferSessionActive},
		remote.FileTransferSpec{
			Direction: fileTransferDirectionDownload, Kind: fileTransferKindFile,
			Pod: "api-0", RemotePath: "/workspace/data.bin",
		}, &output, func(value filestream.ProgressStatus) { progress = value },
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
		context.Background(), testClient{endpoint: websocketURL(server.URL)}, profile.Profile{ID: "server"},
		remote.Session{ID: "session", Namespace: "development", State: fileTransferSessionActive},
		remote.FileTransferSpec{
			Direction: fileTransferDirectionDownload, Kind: fileTransferKindFile,
			Pod: "api-0", RemotePath: "/workspace/data.bin",
		}, failingWriter{}, nil,
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

var _ io.Writer = failingWriter{}
