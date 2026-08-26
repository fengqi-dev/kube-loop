package filetransfer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/protocol/filestream"
)

type waitErrorReader struct {
	wake <-chan struct{}
	err  error
}

func (reader waitErrorReader) Read([]byte) (int, error) {
	<-reader.wake
	return 0, reader.err
}

func TestUploadWaitsForResponseReaderOnLocalReadFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := (&websocket.Upgrader{}).Upgrade(writer, request, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer checkTestClose(t, connection.Close)
		progress, _ := filestream.EncodeProgress(filestream.ProgressStatus{Total: 1})
		if err := connection.WriteMessage(websocket.BinaryMessage, progress); err != nil {
			t.Error(err)
			return
		}
		_, _, _ = connection.ReadMessage()
	}))
	defer server.Close()
	callbackStarted := make(chan struct{})
	releaseCallback := make(chan struct{})
	readErr := errors.New("local read failed")
	returned := make(chan error, 1)
	go func() {
		_, _, err := Upload(
			context.Background(), testClient{endpoint: websocketURL(server.URL)}, profile.Profile{ID: "server"},
			remote.Session{ID: "session", Namespace: "development", State: fileTransferSessionActive},
			remote.FileTransferSpec{
				Direction: fileTransferDirectionUpload, Kind: fileTransferKindFile, Pod: "api-0",
				RemotePath: "/workspace/data.bin", Size: 1,
			},
			waitErrorReader{wake: callbackStarted, err: readErr},
			func(filestream.ProgressStatus) {
				close(callbackStarted)
				<-releaseCallback
			},
		)
		returned <- err
	}()
	select {
	case <-callbackStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("response reader did not report progress")
	}
	select {
	case err := <-returned:
		t.Fatalf("Upload returned before response reader stopped: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseCallback)
	select {
	case err := <-returned:
		if !errors.Is(err, readErr) {
			t.Fatalf("Upload error = %v, want %v", err, readErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Upload did not wait for response reader")
	}
}

func TestUploadStreamsDataWhileReceivingProgressAndVerifiesResult(t *testing.T) {
	contents := bytes.Repeat([]byte("upload-data-"), 40_000)
	checksum := sha256.Sum256(contents)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := (&websocket.Upgrader{}).Upgrade(writer, request, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer checkTestClose(t, connection.Close)
		connection.SetReadLimit(filestream.MaximumData + 1)
		hash := sha256.New()
		var transferred uint64
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
			if frame.Type == filestream.Complete {
				var digest [32]byte
				copy(digest[:], hash.Sum(nil))
				result, _ := filestream.EncodeResult(filestream.TransferResult{
					Status: filestream.ResultSucceeded, Transferred: transferred, Checksum: digest, HasChecksum: true,
				})
				_ = connection.WriteMessage(websocket.BinaryMessage, result)
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
			if err := connection.WriteMessage(websocket.BinaryMessage, progress); err != nil {
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
