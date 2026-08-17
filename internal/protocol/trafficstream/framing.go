// Package trafficstream carries reverse traffic protocol frames as binary
// WebSocket messages inside one tunnel multiplexer stream.
package trafficstream

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	MaximumFrameBytes = 14 + (256 << 10)
	Subprotocol       = "kubeloop.traffic.v1"
	webSocketURL      = "ws://traffic.kubeloop.internal/v1"
	handshakeTimeout  = 10 * time.Second
)

// FrameConn transports one binary WebSocket message at a time. It permits one
// concurrent reader and any number of concurrent writers.
type FrameConn struct {
	conn      *websocket.Conn
	readGate  chan struct{}
	writeGate chan struct{}
}

// Dial upgrades an already-authenticated tunnel multiplexer stream to the
// client side of the Traffic WebSocket protocol.
func Dial(ctx context.Context, connection net.Conn) (*FrameConn, error) {
	if err := validateHandshake(ctx, connection); err != nil {
		return nil, err
	}
	handshakeContext, cancelHandshake := context.WithTimeout(ctx, handshakeTimeout)
	defer cancelHandshake()
	connectionDialer := &singleConnectionDialer{connection: connection}
	dialer := websocket.Dialer{
		NetDialContext:    connectionDialer.DialContext,
		Subprotocols:      []string{Subprotocol},
		EnableCompression: false,
	}
	webSocket, response, err := dialer.DialContext(handshakeContext, webSocketURL, nil)
	if err != nil {
		if response != nil {
			_ = response.Body.Close()
		}
		if contextErr := handshakeContext.Err(); contextErr != nil {
			return nil, contextErr
		}
		if response != nil {
			return nil, fmt.Errorf("dial Traffic WebSocket: status %d: %w", response.StatusCode, err)
		}
		return nil, fmt.Errorf("dial Traffic WebSocket: %w", err)
	}
	if webSocket.Subprotocol() != Subprotocol {
		_ = webSocket.Close()
		return nil, errors.New("Traffic WebSocket subprotocol was not negotiated")
	}
	return newFrameConn(webSocket), nil
}

// Accept upgrades an already-authenticated tunnel multiplexer stream to the
// server side of the Traffic WebSocket protocol.
func Accept(ctx context.Context, connection net.Conn) (*FrameConn, error) {
	if err := validateHandshake(ctx, connection); err != nil {
		return nil, err
	}
	handshakeContext, cancelHandshake := context.WithTimeout(ctx, handshakeTimeout)
	defer cancelHandshake()
	clearDeadline, err := bindHandshakeContext(handshakeContext, connection)
	if err != nil {
		return nil, err
	}
	defer clearDeadline()

	listener := newSingleConnectionListener(connection)
	result := make(chan acceptResult, 1)
	server := &http.Server{
		Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.Method != http.MethodGet || request.URL.Path != "/v1" {
				http.NotFound(writer, request)
				result <- acceptResult{err: errors.New("Traffic WebSocket request is invalid")}
				return
			}
			upgrader := websocket.Upgrader{
				Subprotocols:      []string{Subprotocol},
				EnableCompression: false,
			}
			webSocket, acceptErr := upgrader.Upgrade(writer, request, nil)
			if acceptErr == nil && webSocket.Subprotocol() != Subprotocol {
				_ = webSocket.Close()
				acceptErr = errors.New("Traffic WebSocket subprotocol was not negotiated")
			}
			result <- acceptResult{connection: webSocket, err: acceptErr}
		}),
		BaseContext:    func(net.Listener) context.Context { return handshakeContext },
		MaxHeaderBytes: 8 << 10,
	}
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- server.Serve(listener)
	}()

	var accepted acceptResult
	select {
	case accepted = <-result:
	case <-handshakeContext.Done():
		accepted.err = handshakeContext.Err()
	}
	_ = listener.Close()
	<-serveDone
	if accepted.err != nil {
		if contextErr := handshakeContext.Err(); contextErr != nil {
			return nil, contextErr
		}
		return nil, fmt.Errorf("accept Traffic WebSocket: %w", accepted.err)
	}
	return newFrameConn(accepted.connection), nil
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
		return nil, errors.New("Traffic WebSocket connection is required")
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
		return nil, errors.New("Traffic WebSocket message must be binary")
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
		return errors.New("Traffic WebSocket connection is required")
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
		return errors.New("Traffic WebSocket connection is required")
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

func validateHandshake(ctx context.Context, connection net.Conn) error {
	if ctx == nil {
		return errors.New("traffic stream context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if connection == nil {
		return errors.New("traffic stream connection is required")
	}
	return nil
}

func bindHandshakeContext(ctx context.Context, connection net.Conn) (func(), error) {
	return bindContext(ctx, connection.SetDeadline, "Traffic WebSocket handshake")
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

type singleConnectionDialer struct {
	mu         sync.Mutex
	connection net.Conn
}

func (dialer *singleConnectionDialer) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dialer.mu.Lock()
	defer dialer.mu.Unlock()
	if dialer.connection == nil {
		return nil, errors.New("Traffic WebSocket connection was already consumed")
	}
	connection := dialer.connection
	dialer.connection = nil
	return connection, nil
}

type acceptResult struct {
	connection *websocket.Conn
	err        error
}

type singleConnectionListener struct {
	connection net.Conn
	mu         sync.Mutex
	accepted   bool
	closeOnce  sync.Once
	closed     chan struct{}
}

func newSingleConnectionListener(connection net.Conn) *singleConnectionListener {
	return &singleConnectionListener{connection: connection, closed: make(chan struct{})}
}

func (listener *singleConnectionListener) Accept() (net.Conn, error) {
	listener.mu.Lock()
	if !listener.accepted {
		select {
		case <-listener.closed:
			listener.mu.Unlock()
			return nil, net.ErrClosed
		default:
		}
		listener.accepted = true
		connection := listener.connection
		listener.mu.Unlock()
		return connection, nil
	}
	listener.mu.Unlock()
	<-listener.closed
	return nil, net.ErrClosed
}

func (listener *singleConnectionListener) Close() error {
	listener.closeOnce.Do(func() { close(listener.closed) })
	return nil
}

func (listener *singleConnectionListener) Addr() net.Addr {
	return listener.connection.LocalAddr()
}
