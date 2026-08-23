// Package trafficstream carries reverse traffic protocol frames as binary
// WebSocket messages inside one tunnel multiplexer stream.
package trafficstream

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gorilla/websocket"
)

const (
	MaximumFrameBytes = 14 + (256 << 10)
	Subprotocol       = "kubeloop.traffic.v1"
)

// FrameConn transports one binary WebSocket message at a time. It permits one
// concurrent reader and any number of concurrent writers.
type FrameConn struct {
	conn      *websocket.Conn
	readGate  chan struct{}
	writeGate chan struct{}
}

func newFrameConn(connection *websocket.Conn) *FrameConn {
	connection.SetReadLimit(MaximumFrameBytes)
	return &FrameConn{
		conn:      connection,
		readGate:  newOperationGate(),
		writeGate: newOperationGate(),
	}
}

// ReadFrame reads exactly one binary WebSocket message.
func (connection *FrameConn) ReadFrame(ctx context.Context) ([]byte, error) {
	if connection == nil || connection.conn == nil {
		return nil, errors.New("traffic WebSocket connection is required")
	}
	release, err := acquireOperation(ctx, connection.readGate)
	if err != nil {
		return nil, err
	}
	defer release()

	clearDeadline, err := bindContext(ctx, connection.conn.SetReadDeadline, "read Traffic WebSocket message")
	if err != nil {
		return nil, err
	}
	defer clearDeadline()
	messageType, frame, err := connection.conn.ReadMessage()
	if err != nil {
		return nil, operationError(ctx, "read Traffic WebSocket message", err)
	}
	if messageType != websocket.BinaryMessage {
		_ = connection.conn.Close()
		return nil, errors.New("traffic WebSocket message must be binary")
	}
	if len(frame) == 0 || len(frame) > MaximumFrameBytes {
		_ = connection.conn.Close()
		return nil, fmt.Errorf("traffic frame size %d is outside 1..%d", len(frame), MaximumFrameBytes)
	}
	return frame, nil
}

// WriteFrame writes exactly one binary WebSocket message. Concurrent calls are
// serialized and waiting for the writer is context-aware.
func (connection *FrameConn) WriteFrame(ctx context.Context, frame []byte) error {
	if connection == nil || connection.conn == nil {
		return errors.New("traffic WebSocket connection is required")
	}
	if len(frame) == 0 || len(frame) > MaximumFrameBytes {
		return fmt.Errorf("traffic frame size %d is outside 1..%d", len(frame), MaximumFrameBytes)
	}
	release, err := acquireOperation(ctx, connection.writeGate)
	if err != nil {
		return err
	}
	defer release()
	clearDeadline, err := bindContext(ctx, connection.conn.SetWriteDeadline, "write Traffic WebSocket message")
	if err != nil {
		return err
	}
	defer clearDeadline()
	if err := connection.conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
		return operationError(ctx, "write Traffic WebSocket message", err)
	}
	return nil
}

func (connection *FrameConn) Close() error {
	if connection == nil || connection.conn == nil {
		return errors.New("traffic WebSocket connection is required")
	}
	return connection.conn.Close()
}

func newOperationGate() chan struct{} {
	gate := make(chan struct{}, 1)
	gate <- struct{}{}
	return gate
}

func acquireOperation(ctx context.Context, gate chan struct{}) (func(), error) {
	if ctx == nil {
		return nil, errors.New("traffic stream context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-gate:
		return func() { gate <- struct{}{} }, nil
	}
}

func operationError(ctx context.Context, operation string, err error) error {
	if contextErr := ctx.Err(); contextErr != nil {
		return contextErr
	}
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
		return context.DeadlineExceeded
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func bindContext(ctx context.Context, setDeadline func(time.Time) error, operation string) (func(), error) {
	deadline := time.Time{}
	if contextDeadline, ok := ctx.Deadline(); ok {
		deadline = contextDeadline
	}
	if err := setDeadline(deadline); err != nil {
		return nil, fmt.Errorf("set %s deadline: %w", operation, err)
	}
	interruptFinished := make(chan struct{})
	stopInterrupt := context.AfterFunc(ctx, func() {
		_ = setDeadline(time.Now())
		close(interruptFinished)
	})
	return func() {
		if !stopInterrupt() {
			<-interruptFinished
		}
		_ = setDeadline(time.Time{})
	}, nil
}
