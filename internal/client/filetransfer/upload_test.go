package filetransfer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/protocol/filestream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/websocket"
)

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
			Direction: fileTransferDirectionUpload, Kind: fileTransferKindFile, Pod: "api-0",
			RemotePath: "/workspace/data.bin", Size: uint64(len(contents)),
			Checksum: filestream.FormatChecksum(checksum),
		}, bytes.NewReader(contents), func(value filestream.ProgressStatus) { progress = value },
	)
	if err != nil || task.ID == "" || result.Status != filestream.ResultSucceeded || result.Checksum != checksum ||
		progress.Transferred != uint64(len(contents)) {
		t.Fatalf("task = %#v result = %#v progress = %#v err = %v", task, result, progress, err)
	}
}
