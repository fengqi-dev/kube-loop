package websocketmux

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/wss"
	protocolmux "github.com/fengqi-dev/kube-loop/internal/transport/websocketmux"
	"github.com/gorilla/websocket"
	"github.com/xtaci/smux"
)

const Subprotocol = wss.Subprotocol

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
	SessionID         string
	SessionGeneration uint64
	Logger            *slog.Logger
	SupportedVersions []string
	HandshakeTimeout  time.Duration
	PoolSize          int
	MaxPhysical       int
	MaxStreamsPerConn int
	TrafficEncryption *bool
}

type pooledSession struct {
	ws            *websocket.Conn
	transport     *protocolmux.WebSocketConn
	session       *smux.Session
	maxStreams    int
	correlationID string
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
	logger   *slog.Logger

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
		return nil, errors.New("gateway WebSocket URL must use ws:// or wss://")
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
		config.SupportedVersions = []string{wss.Version}
	} else {
		config.SupportedVersions = append([]string(nil), config.SupportedVersions...)
	}
	if config.HandshakeTimeout <= 0 {
		config.HandshakeTimeout = wss.DefaultHandshakeTimeout
	}
	hello := wss.NewClientHello(config.ClientVersion, config.DeviceID)
	hello.ProtocolVersions = config.SupportedVersions
	if _, err := wss.Encode(hello); err != nil {
		return nil, errors.New("gateway WSS client identity or protocol configuration is invalid")
	}
	if config.HandshakeTimeout > time.Minute {
		return nil, errors.New("gateway WSS handshake timeout must not exceed one minute")
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
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
		return nil, errors.New("gateway multiplexing limits are too large")
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen for local Gateway streams: %w", err)
	}
	forwardCtx, cancel := context.WithCancel(ctx)
	forwarder := &Forwarder{
		ctx: forwardCtx, cancel: cancel, listener: listener, config: config,
		logger: config.Logger.With("component", "client-data-plane"),
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
		if !forwarder.commitSession(session) {
			_ = session.session.Close()
			_ = closeWebSocket(session.ws, websocket.CloseGoingAway, "client closed during session setup")
			_ = listener.Close()
			cancel()
			return nil, net.ErrClosed
		}
	}
	forwarder.wg.Go(forwarder.acceptLoop)
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
			_ = closeWebSocket(item.ws, websocket.CloseNormalClosure, "client shutdown")
		}
		forwarder.wg.Wait()
	})
	return forwarder.closeErr
}
