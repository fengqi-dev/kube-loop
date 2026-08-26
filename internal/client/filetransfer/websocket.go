package filetransfer

import (
	"context"
	"fmt"
	"time"

	"github.com/gorilla/websocket"
)

func readWebSocket(ctx context.Context, connection *websocket.Conn) (int, []byte, error) {
	stop, err := bindWebSocketContext(ctx, connection.SetReadDeadline)
	if err != nil {
		return 0, nil, err
	}
	messageType, payload, err := connection.ReadMessage()
	stop()
	if err != nil && ctx.Err() != nil {
		_ = connection.Close()
		return 0, nil, fmt.Errorf("WebSocket operation: %w", ctx.Err())
	}
	return messageType, payload, err
}

func writeWebSocket(ctx context.Context, connection *websocket.Conn, messageType int, payload []byte) error {
	stop, err := bindWebSocketContext(ctx, connection.SetWriteDeadline)
	if err != nil {
		return err
	}
	err = connection.WriteMessage(messageType, payload)
	stop()
	if err != nil && ctx.Err() != nil {
		_ = connection.Close()
		return fmt.Errorf("WebSocket operation: %w", ctx.Err())
	}
	return err
}

func bindWebSocketContext(ctx context.Context, setDeadline func(time.Time) error) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	deadline, _ := ctx.Deadline()
	if err := setDeadline(deadline); err != nil {
		return nil, err
	}
	fired := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = setDeadline(time.Now())
		close(fired)
	})
	return func() {
		if !stop() {
			<-fired
		}
		_ = setDeadline(time.Time{})
	}, nil
}
