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
	webSocketURL     = "ws://traffic.kubeloop.internal/v1"
	handshakeTimeout = 10 * time.Second
)

// Dial upgrades an already-authenticated tunnel multiplexer stream to the
// client side of the Traffic WebSocket protocol.
func Dial(ctx context.Context, connection net.Conn) (*FrameConn, error) {
	return DialWithEncryption(ctx, connection, true)
}

// DialWithEncryption upgrades a stream and optionally enables the
// Noise_XX_25519_ChaChaPoly_SHA256 transport. Encryption is enabled by default
// by Dial; this explicit variant exists for the compatibility switch.
func DialWithEncryption(ctx context.Context, connection net.Conn, encryptionEnabled bool) (*FrameConn, error) {
	return dialWithEncryptionPrologue(ctx, connection, encryptionEnabled, nil, nil)
}

// DialWithEncryptionPeer authenticates the Gateway's Noise static key against
// the public key carried by the Control Plane RelayTicket.
func DialWithEncryptionPeer(
	ctx context.Context,
	connection net.Conn,
	encryptionEnabled bool,
	expectedPeerStatic []byte,
) (*FrameConn, error) {
	if encryptionEnabled && len(expectedPeerStatic) != NoisePublicKeyBytes {
		return nil, errors.New("Gateway Noise public key is required")
	}
	return dialWithEncryptionPrologue(ctx, connection, encryptionEnabled, expectedPeerStatic, nil)
}

// DialWithEncryptionPrologue additionally binds the Noise handshake to an
// authenticated session secret known by both endpoints.
func DialWithEncryptionPrologue(ctx context.Context, connection net.Conn, encryptionEnabled bool, prologue []byte) (*FrameConn, error) {
	return dialWithEncryptionPrologue(ctx, connection, encryptionEnabled, nil, prologue)
}

func dialWithEncryptionPrologue(
	ctx context.Context,
	connection net.Conn,
	encryptionEnabled bool,
	expectedPeerStatic, prologue []byte,
) (*FrameConn, error) {
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
		return nil, errors.New("traffic WebSocket subprotocol was not negotiated")
	}
	frameConn, err := newSecureFrameConn(
		handshakeContext, webSocket, true, encryptionEnabled, nil, expectedPeerStatic, prologue,
	)
	if err != nil {
		return nil, fmt.Errorf("establish encrypted Traffic WebSocket: %w", err)
	}
	return frameConn, nil
}

// Accept upgrades an already-authenticated tunnel multiplexer stream to the
// server side of the Traffic WebSocket protocol.
func Accept(ctx context.Context, connection net.Conn) (*FrameConn, error) {
	return AcceptWithEncryption(ctx, connection, true)
}

// AcceptWithEncryption accepts a stream and optionally enables the
// Noise_XX_25519_ChaChaPoly_SHA256 transport. Encryption is enabled by default
// by Accept; this explicit variant exists for the compatibility switch.
func AcceptWithEncryption(ctx context.Context, connection net.Conn, encryptionEnabled bool) (*FrameConn, error) {
	return acceptWithEncryptionPrologue(ctx, connection, encryptionEnabled, nil, nil)
}

// AcceptWithEncryptionStatic authenticates the Gateway with its process-scoped
// Noise static keypair.
func AcceptWithEncryptionStatic(
	ctx context.Context,
	connection net.Conn,
	encryptionEnabled bool,
	staticKey NoiseStaticKeypair,
) (*FrameConn, error) {
	if encryptionEnabled && (len(staticKey.Private) != NoisePublicKeyBytes || len(staticKey.Public) != NoisePublicKeyBytes) {
		return nil, errors.New("Gateway Noise static keypair is required")
	}
	return acceptWithEncryptionPrologue(ctx, connection, encryptionEnabled, &staticKey, nil)
}

// AcceptWithEncryptionPrologue additionally binds the Noise handshake to an
// authenticated session secret known by both endpoints.
func AcceptWithEncryptionPrologue(ctx context.Context, connection net.Conn, encryptionEnabled bool, prologue []byte) (*FrameConn, error) {
	return acceptWithEncryptionPrologue(ctx, connection, encryptionEnabled, nil, prologue)
}

func acceptWithEncryptionPrologue(
	ctx context.Context,
	connection net.Conn,
	encryptionEnabled bool,
	staticKey *NoiseStaticKeypair,
	prologue []byte,
) (*FrameConn, error) {
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
				result <- acceptResult{err: errors.New("traffic WebSocket request is invalid")}
				return
			}
			upgrader := websocket.Upgrader{
				Subprotocols:      []string{Subprotocol},
				EnableCompression: false,
			}
			webSocket, acceptErr := upgrader.Upgrade(writer, request, nil)
			if acceptErr == nil && webSocket.Subprotocol() != Subprotocol {
				_ = webSocket.Close()
				acceptErr = errors.New("traffic WebSocket subprotocol was not negotiated")
			}
			result <- acceptResult{connection: webSocket, err: acceptErr}
		}),
		BaseContext:       func(net.Listener) context.Context { return handshakeContext },
		ReadHeaderTimeout: handshakeTimeout,
		MaxHeaderBytes:    8 << 10,
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
	frameConn, err := newSecureFrameConn(
		handshakeContext, accepted.connection, false, encryptionEnabled, staticKey, nil, prologue,
	)
	if err != nil {
		return nil, fmt.Errorf("establish encrypted Traffic WebSocket: %w", err)
	}
	return frameConn, nil
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
		return nil, errors.New("traffic WebSocket connection was already consumed")
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
