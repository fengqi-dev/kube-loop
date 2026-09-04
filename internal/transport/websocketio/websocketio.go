// Package websocketio holds the Gorilla WebSocket plumbing that every
// WebSocket endpoint in this project repeats: upgrading a request, tying read
// and write deadlines to a context so a cancelled caller cannot leave a
// blocked connection behind, and closing with a protocol close frame.
package websocketio

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

// closeGrace bounds how long a close frame may take to leave before the
// connection is torn down regardless.
const closeGrace = 5 * time.Second

// Upgrade turns an HTTP request into a WebSocket connection, echoing back the
// headers the handler has already staged on the response.
func Upgrade(writer http.ResponseWriter, request *http.Request) (*websocket.Conn, error) {
	connection, err := (&websocket.Upgrader{}).Upgrade(writer, request, writer.Header().Clone())
	if err != nil {
		return nil, fmt.Errorf("accept WebSocket: %w", err)
	}
	return connection, nil
}

// Read receives one message, aborting if ctx ends first. A connection torn down
// by cancellation is closed, because its deadline state is no longer usable.
func Read(ctx context.Context, connection *websocket.Conn) (int, []byte, error) {
	stop, err := BindContext(ctx, connection.SetReadDeadline)
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

// Write sends one message under the same cancellation rules as Read.
func Write(ctx context.Context, connection *websocket.Conn, messageType int, payload []byte) error {
	stop, err := BindContext(ctx, connection.SetWriteDeadline)
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

// Close sends a close frame carrying code and reason, then closes the
// connection. Both outcomes are reported so a caller sees a failed handshake
// even when the teardown itself succeeded.
func Close(connection *websocket.Conn, code int, reason string) error {
	writeErr := connection.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(code, reason),
		time.Now().Add(closeGrace),
	)
	return errors.Join(writeErr, connection.Close())
}

// BindContext applies ctx's deadline to the connection and arranges for
// cancellation to expire the deadline, which is the only way to interrupt a
// blocked Gorilla read or write. The returned function must be called once the
// operation returns; it waits for an in-flight cancellation to finish touching
// the deadline before clearing it, so the two never race.
func BindContext(ctx context.Context, setDeadline func(time.Time) error) (func(), error) {
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
