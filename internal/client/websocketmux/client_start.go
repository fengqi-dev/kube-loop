package websocketmux

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fengqi-dev/kube-loop/internal/protocol/wssprotocol"
)

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
	forwarder.wg.Add(1)
	go forwarder.acceptLoop()
	return forwarder, nil
}
