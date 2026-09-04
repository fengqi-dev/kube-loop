package exec

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fengqi-dev/kube-loop/internal/transport/websocketio"
)

type gorillaExecConnection struct{ connection *websocket.Conn }

func (connection gorillaExecConnection) Read(ctx context.Context) (int, []byte, error) {
	stop, err := websocketio.BindContext(ctx, connection.connection.SetReadDeadline)
	if err != nil {
		return 0, nil, err
	}
	messageType, payload, err := connection.connection.ReadMessage()
	stop()
	if err != nil && ctx.Err() != nil {
		_ = connection.connection.Close()
		return 0, nil, fmt.Errorf("WebSocket operation: %w", ctx.Err())
	}
	return messageType, payload, err
}

func (connection gorillaExecConnection) Write(ctx context.Context, messageType int, payload []byte) error {
	stop, err := websocketio.BindContext(ctx, connection.connection.SetWriteDeadline)
	if err != nil {
		return err
	}
	err = connection.connection.WriteMessage(messageType, payload)
	stop()
	if err != nil && ctx.Err() != nil {
		_ = connection.connection.Close()
		return fmt.Errorf("WebSocket operation: %w", ctx.Err())
	}
	return err
}

func (connection gorillaExecConnection) Close(code int, reason string) error {
	writeErr := connection.connection.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(code, reason),
		time.Now().Add(5*time.Second),
	)
	return errors.Join(writeErr, connection.connection.Close())
}
