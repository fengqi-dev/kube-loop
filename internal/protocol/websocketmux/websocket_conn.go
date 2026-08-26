package websocketmux

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// NewWebSocketConn exposes WebSocket messages as a byte stream for smux.
func NewWebSocketConn(ctx context.Context, connection *websocket.Conn, messageType int) *WebSocketConn {
	connection.SetReadLimit(0)
	stream := &WebSocketConn{ctx: ctx, connection: connection, messageType: messageType}
	stream.stopContext = context.AfterFunc(ctx, func() {
		_ = connection.NetConn().SetDeadline(time.Now())
	})
	return stream
}

// WebSocketConn adapts Gorilla message frames to the stream contract required by smux.
type WebSocketConn struct {
	ctx           context.Context
	connection    *websocket.Conn
	messageType   int
	reader        io.Reader
	readEOF       bool
	readMu        sync.Mutex
	writeMu       sync.Mutex
	deadlineMu    sync.Mutex
	writeDeadline time.Time
	closeOnce     sync.Once
	closeErr      error
	stopContext   func() bool
}

func (stream *WebSocketConn) Read(payload []byte) (int, error) {
	if err := stream.ctx.Err(); err != nil {
		return 0, err
	}
	stream.readMu.Lock()
	defer stream.readMu.Unlock()
	for {
		if stream.readEOF {
			return 0, io.EOF
		}
		if stream.reader == nil {
			messageType, reader, err := stream.connection.NextReader()
			if err != nil {
				if contextErr := stream.ctx.Err(); contextErr != nil {
					return 0, contextErr
				}
				if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					stream.readEOF = true
					return 0, io.EOF
				}
				return 0, err
			}
			if messageType != stream.messageType {
				reason := fmt.Sprintf("unexpected WebSocket message type %d", messageType)
				_ = writeClose(stream.connection, websocket.CloseUnsupportedData, reason)
				return 0, errors.New(reason)
			}
			stream.reader = reader
		}
		n, err := stream.reader.Read(payload)
		if errors.Is(err, io.EOF) {
			stream.reader = nil
			if n == 0 {
				continue
			}
			return n, nil
		}
		return n, err
	}
}

func (stream *WebSocketConn) Write(payload []byte) (int, error) {
	if err := stream.ctx.Err(); err != nil {
		return 0, err
	}
	stream.writeMu.Lock()
	defer stream.writeMu.Unlock()
	stream.deadlineMu.Lock()
	deadline := stream.writeDeadline
	stream.deadlineMu.Unlock()
	if err := stream.connection.SetWriteDeadline(deadline); err != nil {
		return 0, err
	}
	if err := stream.connection.WriteMessage(stream.messageType, payload); err != nil {
		if contextErr := stream.ctx.Err(); contextErr != nil {
			return 0, contextErr
		}
		return 0, err
	}
	return len(payload), nil
}

func (stream *WebSocketConn) Close() error {
	stream.closeOnce.Do(func() {
		if stream.stopContext != nil {
			stream.stopContext()
		}
		stream.closeErr = writeClose(stream.connection, websocket.CloseNormalClosure, "")
	})
	return stream.closeErr
}

func (stream *WebSocketConn) LocalAddr() net.Addr  { return stream.connection.NetConn().LocalAddr() }
func (stream *WebSocketConn) RemoteAddr() net.Addr { return stream.connection.NetConn().RemoteAddr() }

func (stream *WebSocketConn) SetDeadline(deadline time.Time) error {
	stream.deadlineMu.Lock()
	stream.writeDeadline = deadline
	stream.deadlineMu.Unlock()
	return stream.connection.NetConn().SetDeadline(deadline)
}

func (stream *WebSocketConn) SetReadDeadline(deadline time.Time) error {
	return stream.connection.SetReadDeadline(deadline)
}

func (stream *WebSocketConn) SetWriteDeadline(deadline time.Time) error {
	stream.deadlineMu.Lock()
	stream.writeDeadline = deadline
	stream.deadlineMu.Unlock()
	return stream.connection.SetWriteDeadline(deadline)
}

func (stream *WebSocketConn) Ping(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	stream.writeMu.Lock()
	defer stream.writeMu.Unlock()
	deadline := time.Now().Add(5 * time.Second)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	fired := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = stream.connection.NetConn().SetWriteDeadline(time.Now())
		close(fired)
	})
	err := stream.connection.WriteControl(websocket.PingMessage, nil, deadline)
	if !stop() {
		<-fired
	}
	_ = stream.connection.NetConn().SetWriteDeadline(time.Time{})
	if err != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func writeClose(connection *websocket.Conn, code int, reason string) error {
	writeErr := connection.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(code, reason),
		time.Now().Add(5*time.Second),
	)
	return errors.Join(writeErr, connection.Close())
}
