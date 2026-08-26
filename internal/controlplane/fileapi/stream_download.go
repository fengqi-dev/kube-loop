package fileapi

import (
	"context"
	"errors"
	"sync"

	"github.com/gorilla/websocket"

	"github.com/fengqi-dev/kube-loop/internal/protocol/filestream"
)

func readDownloadControl(
	ctx context.Context,
	connection *websocket.Conn,
	cancel context.CancelFunc,
) {
	messageType, encoded, err := readWebSocket(ctx, connection)
	if err != nil || messageType != websocket.BinaryMessage {
		cancel()
		return
	}
	frame, err := filestream.Decode(encoded)
	if err != nil || frame.Type != filestream.Cancel {
		cancel()
		return
	}
	cancel()
}

type downloadWriter struct {
	ctx         context.Context
	connection  *websocket.Conn
	mu          *sync.Mutex
	transferred uint64
	total       uint64
	maximum     uint64
}

func (writer *downloadWriter) Write(value []byte) (int, error) {
	written := 0
	for len(value) > 0 {
		length := min(len(value), filestream.MaximumData)
		chunk := value[:length]
		if writer.transferred > writer.maximum ||
			uint64(length) > writer.maximum-writer.transferred {
			return written, errors.New("download exceeds configured size limit")
		}
		encoded, _ := filestream.Encode(
			filestream.Frame{Type: filestream.Data, Payload: chunk},
		)
		writer.mu.Lock()
		err := writeWebSocket(
			writer.ctx, writer.connection,
			websocket.BinaryMessage,
			encoded,
		)
		writer.mu.Unlock()
		if err != nil {
			return written, err
		}
		writer.transferred += uint64(length)
		written += length
		value = value[length:]
		if err := writer.progress(); err != nil {
			return written, err
		}
	}
	return written, nil
}

func (writer *downloadWriter) progress() error {
	return writeProgress(
		writer.ctx,
		writer.connection,
		writer.mu,
		writer.transferred,
		writer.total,
	)
}

func writeProgress(
	ctx context.Context,
	connection *websocket.Conn,
	writeMu *sync.Mutex,
	transferred, total uint64,
) error {
	encoded, err := filestream.EncodeProgress(
		filestream.ProgressStatus{Transferred: transferred, Total: total},
	)
	if err != nil {
		return err
	}
	writeMu.Lock()
	err = writeWebSocket(ctx, connection, websocket.BinaryMessage, encoded)
	writeMu.Unlock()
	return err
}
