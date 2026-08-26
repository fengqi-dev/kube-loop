package filetransfer

import (
	"context"
	"errors"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/protocol/filestream"
)

type Client interface {
	CreateFileTransferTask(
		context.Context,
		profile.Profile,
		remote.Session,
		remote.FileTransferSpec,
		string,
	) (remote.FileTransferTask, error)
	OpenFileTransferStream(
		context.Context,
		profile.Profile,
		remote.Session,
		remote.FileTransferTask,
	) (*websocket.Conn, error)
}

type ProgressFunc func(filestream.ProgressStatus)

func cancel(connection *websocket.Conn) {
	encoded, _ := filestream.Encode(filestream.Frame{Type: filestream.Cancel})
	ctx, stop := context.WithTimeout(context.Background(), 2*time.Second)
	defer stop()
	_ = writeWebSocket(ctx, connection, websocket.BinaryMessage, encoded)
}

func resultError(result filestream.TransferResult) error {
	switch result.Status {
	case filestream.ResultSucceeded:
		return nil
	case filestream.ResultCancelled:
		return context.Canceled
	default:
		if result.Error == "" {
			return errors.New("gateway file transfer failed")
		}
		return errors.New(result.Error)
	}
}

func equalDigest(checksum [32]byte, value []byte) bool {
	return len(value) == len(checksum) && string(checksum[:]) == string(value)
}
