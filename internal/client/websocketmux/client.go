package websocketmux

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/streamcopy"
	"github.com/fengqi-dev/kube-loop/internal/protocol/websocket"
	protocolmux "github.com/fengqi-dev/kube-loop/internal/protocol/websocketmux"
	"github.com/fengqi-dev/kube-loop/internal/protocol/wssprotocol"
	"github.com/xtaci/smux"
)

const Subprotocol = wssprotocol.Subprotocol

const (
	defaultPoolSize          = 2
	defaultMaxPhysical       = 4
	defaultMaxStreams        = 128
	defaultKeepAliveInterval = 15 * time.Second
	defaultKeepAliveTimeout  = 45 * time.Second
	maxPoolSize              = 8
	maxPhysicalConnections   = 16
	maxStreamsPerConnection  = 1024
)

type ClientConfig struct {
	URL               string
	Token             string
	TokenSource       func(context.Context) (string, error)
	TLSConfig         *tls.Config
	ClientVersion     string
	DeviceID          string
	SupportedVersions []string
	HandshakeTimeout  time.Duration
	PoolSize          int
	MaxPhysical       int
	MaxStreamsPerConn int
}

type pooledSession struct {
	ws         *websocket.Conn
	session    *smux.Session
	maxStreams int
}

// HandshakeError is returned when the Gateway explicitly rejects WSS v2
// negotiation. Code is stable and can be mapped to a client upgrade or
// re-authentication action without parsing human-readable text.
type HandshakeError struct {
	Code              string
	Message           string
	SupportedVersions []string
}

func (err *HandshakeError) Error() string {
	if err == nil {
		return ""
	}
	if err.Message == "" {
		return "Gateway rejected WSS handshake: " + err.Code
	}
	return "Gateway rejected WSS handshake: " + err.Code + ": " + err.Message
}

// Forwarder exposes a loopback TCP endpoint and maps each accepted connection
// to an independent smux stream over a small pool of WebSocket connections.
type Forwarder struct {
	ctx      context.Context
	cancel   context.CancelFunc
	listener net.Listener
	config   ClientConfig

	mu          sync.Mutex
	sessions    []*pooledSession
	maxPhysical int
	locals      map[net.Conn]struct{}
	streams     map[net.Conn]struct{}
	closed      bool
	dialMu      sync.Mutex
	openGate    chan struct{}
	closeOnce   sync.Once
	closeErr    error
	wg          sync.WaitGroup
}

func Start(ctx context.Context, config ClientConfig) (*Forwarder, error) {
	parsed, err := url.ParseRequestURI(config.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid Gateway WebSocket URL: %w", err)
	}
	if (parsed.Scheme != "ws" && parsed.Scheme != "wss") || parsed.Host == "" {
		return nil, errors.New("Gateway WebSocket URL must use ws:// or wss://")
	}
	if (config.Token == "") == (config.TokenSource == nil) {
		return nil, errors.New("exactly one Gateway WebSocket token source is required")
	}
	config.ClientVersion = strings.TrimSpace(config.ClientVersion)
	if config.ClientVersion == "" {
		config.ClientVersion = "dev"
	}
	config.DeviceID = strings.TrimSpace(config.DeviceID)
	if len(config.SupportedVersions) == 0 {
		config.SupportedVersions = []string{wssprotocol.Version}
	} else {
		config.SupportedVersions = append([]string(nil), config.SupportedVersions...)
	}
	if config.HandshakeTimeout <= 0 {
		config.HandshakeTimeout = wssprotocol.DefaultHandshakeTimeout
	}
	hello := wssprotocol.NewClientHello(config.ClientVersion, config.DeviceID)
	hello.ProtocolVersions = config.SupportedVersions
	if _, err := wssprotocol.Encode(hello); err != nil {
		return nil, errors.New("Gateway WSS client identity or protocol configuration is invalid")
	}
	if config.HandshakeTimeout > time.Minute {
		return nil, errors.New("Gateway WSS handshake timeout must not exceed one minute")
	}
	if config.PoolSize <= 0 {
		config.PoolSize = defaultPoolSize
	}
	if config.MaxPhysical <= 0 {
		config.MaxPhysical = defaultMaxPhysical
	}
	if config.MaxPhysical < config.PoolSize {
		config.MaxPhysical = config.PoolSize
	}
	if config.MaxStreamsPerConn <= 0 {
		config.MaxStreamsPerConn = defaultMaxStreams
	}
	if config.PoolSize > maxPoolSize || config.MaxPhysical > maxPhysicalConnections ||
		config.MaxStreamsPerConn > maxStreamsPerConnection {
		return nil, errors.New("Gateway multiplexing limits are too large")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen for local Gateway streams: %w", err)
	}
	forwardCtx, cancel := context.WithCancel(ctx)
	forwarder := &Forwarder{
		ctx: forwardCtx, cancel: cancel, listener: listener, config: config,
		locals: make(map[net.Conn]struct{}), streams: make(map[net.Conn]struct{}),
		openGate: make(chan struct{}, 1), maxPhysical: config.MaxPhysical,
	}
	forwarder.openGate <- struct{}{}
	for range config.PoolSize {
		forwarder.mu.Lock()
		atCapacity := len(forwarder.sessions) >= forwarder.maxPhysical
		forwarder.mu.Unlock()
		if atCapacity {
			break
		}
		session, dialErr := forwarder.dial()
		if dialErr != nil {
			if len(forwarder.sessions) == 0 {
				_ = listener.Close()
				cancel()
				return nil, dialErr
			}
			break
		}
		forwarder.sessions = append(forwarder.sessions, session)
	}
	forwarder.wg.Add(1)
	go forwarder.acceptLoop()
	return forwarder, nil
}

func (forwarder *Forwarder) Address() string { return forwarder.listener.Addr().String() }

func (forwarder *Forwarder) Close() error {
	forwarder.closeOnce.Do(func() {
		forwarder.cancel()
		forwarder.closeErr = forwarder.listener.Close()
		forwarder.mu.Lock()
		forwarder.closed = true
		sessions := append([]*pooledSession(nil), forwarder.sessions...)
		forwarder.sessions = nil
		locals := make([]net.Conn, 0, len(forwarder.locals))
		for connection := range forwarder.locals {
			locals = append(locals, connection)
		}
		forwarder.locals = make(map[net.Conn]struct{})
		streams := make([]net.Conn, 0, len(forwarder.streams))
		for connection := range forwarder.streams {
			streams = append(streams, connection)
		}
		forwarder.streams = make(map[net.Conn]struct{})
		forwarder.mu.Unlock()
		for _, connection := range locals {
			_ = connection.Close()
		}
		for _, connection := range streams {
			_ = connection.Close()
		}
		for _, item := range sessions {
			_ = item.session.Close()
			_ = item.ws.Close(websocket.StatusNormalClosure, "client shutdown")
		}
		forwarder.wg.Wait()
	})
	return forwarder.closeErr
}

// OpenStream opens one tracked logical connection on the existing WebSocket
// pool. Closing the Forwarder (including during Data Plane recovery) closes
// every connection returned by this method.
func (forwarder *Forwarder) OpenStream(ctx context.Context) (net.Conn, error) {
	if ctx == nil {
		return nil, errors.New("Gateway logical stream context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-forwarder.ctx.Done():
		return nil, net.ErrClosed
	case <-forwarder.openGate:
	}
	defer func() { forwarder.openGate <- struct{}{} }()

	stream, err := forwarder.openStream()
	if err != nil {
		return nil, err
	}
	connection := &trackedConn{Conn: protocolmux.NewStreamConn(stream)}
	connection.onClose = func() {
		forwarder.mu.Lock()
		delete(forwarder.streams, connection)
		forwarder.mu.Unlock()
	}
	forwarder.mu.Lock()
	if forwarder.closed {
		forwarder.mu.Unlock()
		_ = connection.Close()
		return nil, net.ErrClosed
	}
	forwarder.streams[connection] = struct{}{}
	forwarder.mu.Unlock()
	if err := ctx.Err(); err != nil {
		_ = connection.Close()
		return nil, err
	}
	return connection, nil
}

type trackedConn struct {
	net.Conn
	onClose func()
	once    sync.Once
	err     error
}

func (connection *trackedConn) Close() error {
	connection.once.Do(func() {
		connection.err = connection.Conn.Close()
		if connection.onClose != nil {
			connection.onClose()
		}
	})
	return connection.err
}

func (forwarder *Forwarder) acceptLoop() {
	defer forwarder.wg.Done()
	for {
		connection, err := forwarder.listener.Accept()
		if err != nil {
			return
		}
		forwarder.mu.Lock()
		if forwarder.closed {
			forwarder.mu.Unlock()
			_ = connection.Close()
			continue
		}
		forwarder.locals[connection] = struct{}{}
		forwarder.wg.Add(1)
		forwarder.mu.Unlock()
		go func() {
			defer forwarder.wg.Done()
			defer func() {
				forwarder.mu.Lock()
				delete(forwarder.locals, connection)
				forwarder.mu.Unlock()
			}()
			forwarder.forward(connection)
		}()
	}
}

func (forwarder *Forwarder) forward(local net.Conn) {
	stream, err := forwarder.openStream()
	if err != nil {
		_ = local.Close()
		return
	}
	defer stream.Close()
	defer local.Close()
	streamcopy.Bidirectional(local, protocolmux.NewStreamConn(stream))
}

func (forwarder *Forwarder) openStream() (*smux.Stream, error) {
	for attempt := range 2 {
		item := forwarder.pickSession()
		if item != nil {
			stream, err := item.session.OpenStream()
			if err == nil {
				return stream, nil
			}
			forwarder.discard(item)
		}
		if _, err := forwarder.ensureSession(); err != nil && attempt == 1 {
			return nil, err
		}
	}
	return nil, errors.New("no healthy Gateway WebSocket session")
}

func (forwarder *Forwarder) pickSession() *pooledSession {
	forwarder.mu.Lock()
	defer forwarder.mu.Unlock()
	var selected *pooledSession
	for _, item := range forwarder.sessions {
		if item.session.IsClosed() || item.session.NumStreams() >= item.maxStreams {
			continue
		}
		if selected == nil || item.session.NumStreams() < selected.session.NumStreams() {
			selected = item
		}
	}
	return selected
}

func (forwarder *Forwarder) ensureSession() (*pooledSession, error) {
	forwarder.dialMu.Lock()
	defer forwarder.dialMu.Unlock()
	if item := forwarder.pickSession(); item != nil {
		return item, nil
	}
	forwarder.mu.Lock()
	count := len(forwarder.sessions)
	forwarder.mu.Unlock()
	forwarder.mu.Lock()
	maximum := forwarder.maxPhysical
	forwarder.mu.Unlock()
	if count >= maximum {
		return nil, errors.New("all Gateway WebSocket sessions are at capacity")
	}
	item, err := forwarder.dial()
	if err != nil {
		return nil, err
	}
	forwarder.mu.Lock()
	forwarder.sessions = append(forwarder.sessions, item)
	forwarder.mu.Unlock()
	return item, nil
}

func (forwarder *Forwarder) discard(target *pooledSession) {
	forwarder.mu.Lock()
	for index, item := range forwarder.sessions {
		if item == target {
			forwarder.sessions = append(forwarder.sessions[:index], forwarder.sessions[index+1:]...)
			break
		}
	}
	forwarder.mu.Unlock()
	_ = target.session.Close()
	_ = target.ws.Close(websocket.StatusGoingAway, "session replaced")
}

func (forwarder *Forwarder) dial() (*pooledSession, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if forwarder.config.TLSConfig != nil {
		transport.TLSClientConfig = forwarder.config.TLSConfig.Clone()
	}
	httpClient := &http.Client{Transport: transport}
	dialCtx, cancel := context.WithTimeout(forwarder.ctx, 15*time.Second)
	defer cancel()
	token := forwarder.config.Token
	if forwarder.config.TokenSource != nil {
		var err error
		token, err = forwarder.config.TokenSource(dialCtx)
		if err != nil {
			return nil, fmt.Errorf("obtain Gateway WebSocket token: %w", err)
		}
	}
	if token == "" || strings.TrimSpace(token) != token || strings.ContainsAny(token, "\r\n") {
		return nil, errors.New("Gateway WebSocket token source returned an invalid token")
	}
	header := make(http.Header)
	header.Set("Authorization", "Bearer "+token)
	connection, response, err := websocket.Dial(dialCtx, forwarder.config.URL, &websocket.DialOptions{
		HTTPClient: httpClient, HTTPHeader: header, Subprotocols: []string{Subprotocol},
	})
	if err != nil {
		if response != nil {
			return nil, fmt.Errorf("dial Gateway WebSocket: HTTP %s: %w", response.Status, err)
		}
		return nil, fmt.Errorf("dial Gateway WebSocket: %w", err)
	}
	if connection.Subprotocol() != Subprotocol {
		_ = connection.Close(websocket.StatusPolicyViolation, "subprotocol required")
		return nil, fmt.Errorf(
			"Gateway did not negotiate multiplexing subprotocol %q (got %q)",
			Subprotocol, connection.Subprotocol(),
		)
	}
	if response == nil || response.Header.Get(wssprotocol.VersionHeader) != wssprotocol.Version {
		_ = connection.Close(websocket.StatusPolicyViolation, wssprotocol.CodeVersionMismatch)
		return nil, &HandshakeError{
			Code:              wssprotocol.CodeVersionMismatch,
			Message:           "Gateway does not advertise the WSS ClientHello contract",
			SupportedVersions: []string{wssprotocol.Version},
		}
	}
	connection.SetReadLimit(wssprotocol.MaximumHandshakeBytes)
	handshakeCtx, cancelHandshake := context.WithTimeout(dialCtx, forwarder.config.HandshakeTimeout)
	hello := wssprotocol.NewClientHello(forwarder.config.ClientVersion, forwarder.config.DeviceID)
	hello.ProtocolVersions = append([]string(nil), forwarder.config.SupportedVersions...)
	if err := wssprotocol.Write(handshakeCtx, connection, hello); err != nil {
		cancelHandshake()
		_ = connection.Close(websocket.StatusPolicyViolation, "HANDSHAKE_FAILED")
		return nil, fmt.Errorf("send Gateway WSS ClientHello: %w", err)
	}
	message, err := wssprotocol.Read(handshakeCtx, connection)
	cancelHandshake()
	if err != nil {
		_ = connection.Close(websocket.StatusPolicyViolation, "HANDSHAKE_FAILED")
		if errors.Is(err, wssprotocol.ErrInvalidHandshake) {
			return nil, &HandshakeError{
				Code: wssprotocol.CodeInvalidHandshake, Message: "Gateway returned an invalid WSS handshake",
			}
		}
		return nil, fmt.Errorf("read Gateway WSS handshake: %w", err)
	}
	if message.Reject != nil {
		rejection := message.Reject
		_ = connection.Close(websocket.StatusPolicyViolation, rejection.Code)
		return nil, &HandshakeError{
			Code: rejection.Code, Message: rejection.Message,
			SupportedVersions: append([]string(nil), rejection.SupportedVersions...),
		}
	}
	serverHello := message.ServerHello
	if serverHello == nil || !slices.Contains(hello.ProtocolVersions, serverHello.ProtocolVersion) ||
		!slices.Contains(serverHello.Capabilities, "smux.v2") ||
		!slices.Contains(serverHello.Capabilities, "tunnel.open.v2") ||
		!slices.Contains(serverHello.Capabilities, wssprotocol.CapabilityTrafficWebSocket) {
		_ = connection.Close(websocket.StatusPolicyViolation, "INVALID_HANDSHAKE")
		return nil, errors.New("Gateway returned an incompatible WSS ServerHello")
	}
	maximumStreams := min(forwarder.config.MaxStreamsPerConn, serverHello.Limits.MaximumStreamsPerConnection)
	forwarder.mu.Lock()
	forwarder.maxPhysical = min(forwarder.maxPhysical, serverHello.Limits.MaximumConnectionsPerUser)
	forwarder.mu.Unlock()
	streamConn := websocket.NetConn(forwarder.ctx, connection, websocket.MessageBinary)
	connection.SetReadLimit(serverHello.Limits.MaximumFrameBytes)
	session, err := smux.Client(streamConn, smuxConfig())
	if err != nil {
		_ = connection.Close(websocket.StatusInternalError, "multiplexer setup failed")
		return nil, fmt.Errorf("start Gateway multiplexer: %w", err)
	}
	item := &pooledSession{ws: connection, session: session, maxStreams: maximumStreams}
	forwarder.wg.Add(1)
	go forwarder.keepAlive(item)
	return item, nil
}

func (forwarder *Forwarder) keepAlive(item *pooledSession) {
	defer forwarder.wg.Done()
	ticker := time.NewTicker(defaultKeepAliveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-forwarder.ctx.Done():
			return
		case <-item.session.CloseChan():
			forwarder.discard(item)
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(forwarder.ctx, 10*time.Second)
			err := item.ws.Ping(ctx)
			cancel()
			if err != nil {
				forwarder.discard(item)
				return
			}
		}
	}
}

func smuxConfig() *smux.Config {
	config := smux.DefaultConfig()
	config.Version = 2
	config.KeepAliveInterval = defaultKeepAliveInterval
	config.KeepAliveTimeout = defaultKeepAliveTimeout
	config.MaxReceiveBuffer = 4 * 1024 * 1024
	config.MaxStreamBuffer = 512 * 1024
	return config
}
