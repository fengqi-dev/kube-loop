package websocket

import (
	"context"
	"errors"
	"time"

	gorilla "github.com/gorilla/websocket"
)

func newConn(connection *gorilla.Conn) *Conn {
	return &Conn{raw: connection}
}

func (connection *Conn) Read(ctx context.Context) (MessageType, []byte, error) {
	if ctx == nil {
		return 0, nil, errors.New("WebSocket read context is required")
	}
	connection.readMu.Lock()
	defer connection.readMu.Unlock()
	if err := prepareContextDeadline(ctx, connection.raw.SetReadDeadline); err != nil {
		return 0, nil, err
	}
	stop := interruptOnCancel(ctx, connection.raw.SetReadDeadline)
	messageType, payload, err := connection.raw.ReadMessage()
	finishContextDeadline(stop, connection.raw.SetReadDeadline)
	if err != nil {
		if ctx.Err() != nil {
			_ = connection.raw.Close()
		}
		return 0, nil, operationError(ctx, err)
	}
	return messageType, payload, nil
}

func (connection *Conn) Write(ctx context.Context, messageType MessageType, payload []byte) error {
	if ctx == nil {
		return errors.New("WebSocket write context is required")
	}
	connection.writeMu.Lock()
	defer connection.writeMu.Unlock()
	if err := prepareContextDeadline(ctx, connection.raw.SetWriteDeadline); err != nil {
		return err
	}
	stop := interruptOnCancel(ctx, connection.raw.NetConn().SetWriteDeadline)
	err := connection.raw.WriteMessage(messageType, payload)
	finishContextDeadline(stop, connection.raw.NetConn().SetWriteDeadline)
	_ = connection.raw.SetWriteDeadline(time.Time{})
	if err != nil && ctx.Err() != nil {
		_ = connection.raw.Close()
	}
	return operationError(ctx, err)
}

func (connection *Conn) Ping(ctx context.Context) error {
	if ctx == nil {
		return errors.New("WebSocket ping context is required")
	}
	connection.writeMu.Lock()
	defer connection.writeMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	stop := interruptOnCancel(ctx, connection.raw.NetConn().SetWriteDeadline)
	err := connection.raw.WriteControl(gorilla.PingMessage, nil, deadline)
	finishContextDeadline(stop, connection.raw.NetConn().SetWriteDeadline)
	return operationError(ctx, err)
}

func (connection *Conn) Close(code StatusCode, reason string) error {
	connection.closeMu.Lock()
	if connection.closed {
		connection.closeMu.Unlock()
		return nil
	}
	connection.closed = true
	connection.closeMu.Unlock()

	writeErr := connection.raw.WriteControl(
		gorilla.CloseMessage,
		gorilla.FormatCloseMessage(code, reason),
		time.Now().Add(5*time.Second),
	)
	closeErr := connection.raw.Close()
	return errors.Join(writeErr, closeErr)
}

func (connection *Conn) CloseNow() error {
	connection.closeMu.Lock()
	if connection.closed {
		connection.closeMu.Unlock()
		return nil
	}
	connection.closed = true
	connection.closeMu.Unlock()
	return connection.raw.Close()
}

func (connection *Conn) SetReadLimit(limit int64) {
	if limit < 0 {
		limit = 0
	}
	connection.raw.SetReadLimit(limit)
}

func (connection *Conn) Subprotocol() string {
	return connection.raw.Subprotocol()
}

func CloseStatus(err error) StatusCode {
	if closeError, ok := errors.AsType[*gorilla.CloseError](err); ok {
		return closeError.Code
	}
	return -1
}
