package wssprotocol

import (
	"context"
	"fmt"
	"time"

	"github.com/gorilla/websocket"
)

func Read(ctx context.Context, connection *websocket.Conn) (Message, error) {
	if connection == nil {
		return Message{}, ErrInvalidHandshake
	}
	stop, err := bindContext(ctx, connection.SetReadDeadline)
	if err != nil {
		return Message{}, fmt.Errorf("read WSS handshake message: %w", err)
	}
	messageType, raw, err := connection.ReadMessage()
	stop()
	if err != nil {
		return Message{}, fmt.Errorf("read WSS handshake message: %w", err)
	}
	if messageType != websocket.BinaryMessage {
		return Message{}, ErrInvalidHandshake
	}
	return Decode(raw)
}

func Write(ctx context.Context, connection *websocket.Conn, message any) error {
	raw, err := Encode(message)
	if err != nil {
		return err
	}
	stop, err := bindContext(ctx, connection.SetWriteDeadline)
	if err != nil {
		return fmt.Errorf("write WSS handshake: %w", err)
	}
	err = connection.WriteMessage(websocket.BinaryMessage, raw)
	stop()
	if err != nil {
		return fmt.Errorf("write WSS handshake: %w", err)
	}
	return nil
}

func bindContext(ctx context.Context, setDeadline func(time.Time) error) (func(), error) {
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
