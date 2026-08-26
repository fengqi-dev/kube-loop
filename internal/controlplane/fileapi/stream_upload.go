package fileapi

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/gorilla/websocket"

	"github.com/fengqi-dev/kube-loop/internal/protocol/filestream"
)

func readUpload(
	ctx context.Context,
	connection *websocket.Conn,
	pipe *io.PipeWriter,
	writeMu *sync.Mutex,
	spec Spec,
) error {
	defer func() { _ = pipe.Close() }()
	transferred := spec.Offset
	for {
		messageType, encoded, err := readWebSocket(ctx, connection)
		if err != nil {
			_ = pipe.CloseWithError(err)
			return err
		}
		if messageType != websocket.BinaryMessage {
			err := errors.New("file upload requires binary frames")
			_ = pipe.CloseWithError(err)
			return err
		}
		frame, err := filestream.Decode(encoded)
		if err != nil {
			_ = pipe.CloseWithError(err)
			return err
		}
		switch frame.Type {
		case filestream.Data:
			if transferred > spec.Size ||
				uint64(len(frame.Payload)) > spec.Size-transferred {
				err := errors.New("upload exceeds declared size")
				_ = pipe.CloseWithError(err)
				return err
			}
			if _, err := pipe.Write(frame.Payload); err != nil {
				return err
			}
			transferred += uint64(len(frame.Payload))
			if err := writeProgress(ctx, connection, writeMu, transferred, spec.Size); err != nil {
				return err
			}
		case filestream.Complete:
			if transferred != spec.Size {
				err := errors.New("upload completed before declared size")
				_ = pipe.CloseWithError(err)
				return err
			}
			return nil
		case filestream.Cancel:
			_ = pipe.CloseWithError(context.Canceled)
			return context.Canceled
		default:
			err := errors.New("client sent a server-only file transfer frame")
			_ = pipe.CloseWithError(err)
			return err
		}
	}
}
