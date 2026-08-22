package websocket

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

func NetConn(ctx context.Context, connection *Conn, messageType MessageType) net.Conn {
	if ctx == nil {
		ctx = context.Background()
	}
	connection.SetReadLimit(-1)
	stream := &streamConn{ctx: ctx, connection: connection, messageType: messageType}
	stream.stopContext = context.AfterFunc(ctx, func() {
		_ = connection.raw.NetConn().SetDeadline(time.Now())
	})
	return stream
}

type streamConn struct {
	ctx           context.Context
	connection    *Conn
	messageType   MessageType
	reader        io.Reader
	readEOF       bool
	deadlineMu    sync.Mutex
	writeDeadline time.Time
	closeOnce     sync.Once
	closeErr      error
	stopContext   func() bool
}

func (stream *streamConn) Read(payload []byte) (int, error) {
	if err := stream.ctx.Err(); err != nil {
		return 0, err
	}
	stream.connection.readMu.Lock()
	defer stream.connection.readMu.Unlock()
	for {
		if stream.readEOF {
			return 0, io.EOF
		}
		if stream.reader == nil {
			messageType, reader, err := stream.connection.raw.NextReader()
			if err != nil {
				if contextErr := stream.ctx.Err(); contextErr != nil {
					return 0, contextErr
				}
				if code := CloseStatus(err); code == StatusNormalClosure || code == StatusGoingAway {
					stream.readEOF = true
					return 0, io.EOF
				}
				return 0, err
			}
			if messageType != stream.messageType {
				reason := fmt.Sprintf("unexpected WebSocket message type %d", messageType)
				_ = stream.connection.Close(StatusUnsupportedData, reason)
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

func (stream *streamConn) Write(payload []byte) (int, error) {
	if err := stream.ctx.Err(); err != nil {
		return 0, err
	}
	stream.connection.writeMu.Lock()
	defer stream.connection.writeMu.Unlock()
	stream.deadlineMu.Lock()
	deadline := stream.writeDeadline
	stream.deadlineMu.Unlock()
	if err := stream.connection.raw.SetWriteDeadline(deadline); err != nil {
		return 0, err
	}
	if err := stream.connection.raw.WriteMessage(stream.messageType, payload); err != nil {
		if contextErr := stream.ctx.Err(); contextErr != nil {
			return 0, contextErr
		}
		return 0, err
	}
	return len(payload), nil
}

func (stream *streamConn) Close() error {
	stream.closeOnce.Do(func() {
		if stream.stopContext != nil {
			stream.stopContext()
		}
		stream.closeErr = stream.connection.Close(StatusNormalClosure, "")
	})
	return stream.closeErr
}

func (stream *streamConn) LocalAddr() net.Addr  { return stream.connection.raw.NetConn().LocalAddr() }
func (stream *streamConn) RemoteAddr() net.Addr { return stream.connection.raw.NetConn().RemoteAddr() }

func (stream *streamConn) SetDeadline(deadline time.Time) error {
	stream.deadlineMu.Lock()
	stream.writeDeadline = deadline
	stream.deadlineMu.Unlock()
	return stream.connection.raw.NetConn().SetDeadline(deadline)
}

func (stream *streamConn) SetReadDeadline(deadline time.Time) error {
	return stream.connection.raw.NetConn().SetReadDeadline(deadline)
}

func (stream *streamConn) SetWriteDeadline(deadline time.Time) error {
	stream.deadlineMu.Lock()
	stream.writeDeadline = deadline
	stream.deadlineMu.Unlock()
	return stream.connection.raw.NetConn().SetWriteDeadline(deadline)
}
