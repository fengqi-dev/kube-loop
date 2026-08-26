package kubeapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

func upgradeWebSocket(writer http.ResponseWriter, request *http.Request) (*websocket.Conn, error) {
	connection, err := (&websocket.Upgrader{}).Upgrade(writer, request, writer.Header().Clone())
	if err != nil {
		return nil, fmt.Errorf("accept WebSocket: %w", err)
	}
	return connection, nil
}

func writeWebSocket(ctx context.Context, connection *websocket.Conn, messageType int, payload []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	deadline, _ := ctx.Deadline()
	if err := connection.SetWriteDeadline(deadline); err != nil {
		return err
	}
	fired := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = connection.SetWriteDeadline(time.Now())
		close(fired)
	})
	err := connection.WriteMessage(messageType, payload)
	if !stop() {
		<-fired
	}
	_ = connection.SetWriteDeadline(time.Time{})
	if err != nil && ctx.Err() != nil {
		_ = connection.Close()
		return fmt.Errorf("WebSocket operation: %w", ctx.Err())
	}
	return err
}

func closeWebSocket(connection *websocket.Conn, code int, reason string) error {
	writeErr := connection.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(code, reason),
		time.Now().Add(5*time.Second),
	)
	return errors.Join(writeErr, connection.Close())
}
