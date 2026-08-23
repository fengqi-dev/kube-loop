package wssprotocol

import (
	"context"
	"fmt"

	"github.com/fengqi-dev/kube-loop/internal/protocol/websocket"
)

func Read(ctx context.Context, connection *websocket.Conn) (Message, error) {
	if connection == nil {
		return Message{}, ErrInvalidHandshake
	}
	messageType, raw, err := connection.Read(ctx)
	if err != nil {
		return Message{}, fmt.Errorf("read WSS handshake message: %w", err)
	}
	if messageType != websocket.MessageBinary {
		return Message{}, ErrInvalidHandshake
	}
	return Decode(raw)
}

func Write(ctx context.Context, connection *websocket.Conn, message any) error {
	raw, err := Encode(message)
	if err != nil {
		return err
	}
	if err := connection.Write(ctx, websocket.MessageBinary, raw); err != nil {
		return fmt.Errorf("write WSS handshake: %w", err)
	}
	return nil
}
