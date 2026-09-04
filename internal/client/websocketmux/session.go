package websocketmux

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/middleware"
	"github.com/fengqi-dev/kube-loop/internal/protocol/wss"
	protocolmux "github.com/fengqi-dev/kube-loop/internal/transport/websocketmux"
	"github.com/gorilla/websocket"
	"github.com/xtaci/smux"
)

func newWebSocketDialer(transport *http.Transport) websocket.Dialer {
	dialer := websocket.Dialer{
		Proxy:             transport.Proxy,
		NetDialContext:    transport.DialContext,
		NetDialTLSContext: transport.DialTLSContext,
	}
	if transport.TLSClientConfig != nil {
		dialer.TLSClientConfig = transport.TLSClientConfig.Clone()
		dialer.TLSClientConfig.NextProtos = []string{"http/1.1"}
	}
	return dialer
}

func closeWebSocket(connection *websocket.Conn, code int, reason string) error {
	writeErr := connection.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(code, reason),
		time.Now().Add(5*time.Second),
	)
	return errors.Join(writeErr, connection.Close())
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

func (forwarder *Forwarder) dial() (result *pooledSession, resultErr error) {
	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("default HTTP transport %T is unsupported", http.DefaultTransport)
	}
	transport := defaultTransport.Clone()
	if forwarder.config.TLSConfig != nil {
		transport.TLSClientConfig = forwarder.config.TLSConfig.Clone()
	}
	dialCtx, cancel := context.WithTimeout(forwarder.ctx, 15*time.Second)
	defer cancel()
	dialCtx, correlationID := middleware.Ensure(dialCtx)
	startedAt := time.Now()
	forwarder.logger.InfoContext(
		dialCtx, "Gateway WebSocket dial started",
		"operation", "gateway.websocket.connect",
		"outcome", "started",
		"correlation_id", correlationID,
		"session_id", forwarder.config.SessionID,
		"session_generation", forwarder.config.SessionGeneration,
	)
	defer func() {
		level := slog.LevelInfo
		attributes := []any{
			"operation", "gateway.websocket.connect",
			"outcome", "success",
			"correlation_id", correlationID,
			"duration_ms", time.Since(startedAt).Milliseconds(),
			"session_id", forwarder.config.SessionID,
			"session_generation", forwarder.config.SessionGeneration,
		}
		if resultErr != nil {
			level = slog.LevelError
			attributes[3] = "failure"
			attributes = append(attributes, "error", resultErr)
		}
		forwarder.logger.Log(dialCtx, level, "Gateway WebSocket dial completed", attributes...)
	}()
	token := forwarder.config.Token
	if forwarder.config.TokenSource != nil {
		var err error
		token, err = forwarder.config.TokenSource(dialCtx)
		if err != nil {
			return nil, fmt.Errorf("obtain Gateway WebSocket token: %w", err)
		}
	}
	if token == "" || strings.TrimSpace(token) != token || strings.ContainsAny(token, "\r\n") {
		return nil, errors.New("gateway WebSocket token source returned an invalid token")
	}
	header := make(http.Header)
	header.Set("Authorization", "Bearer "+token)
	header.Set(middleware.Header, correlationID)
	dialer := newWebSocketDialer(transport)
	dialer.Subprotocols = []string{Subprotocol}
	connection, response, err := dialer.DialContext(dialCtx, forwarder.config.URL, header)
	if err != nil {
		if response != nil {
			return nil, errors.Join(
				fmt.Errorf("dial Gateway WebSocket: HTTP %s: %w", response.Status, err),
				response.Body.Close(),
			)
		}
		return nil, fmt.Errorf("dial Gateway WebSocket: %w", err)
	}
	if connection.Subprotocol() != Subprotocol {
		_ = closeWebSocket(connection, websocket.ClosePolicyViolation, "subprotocol required")
		return nil, fmt.Errorf(
			"gateway did not negotiate multiplexing subprotocol %q (got %q)",
			Subprotocol, connection.Subprotocol(),
		)
	}
	if response == nil || response.Header.Get(wss.VersionHeader) != wss.Version {
		_ = closeWebSocket(connection, websocket.ClosePolicyViolation, wss.CodeVersionMismatch)
		return nil, &HandshakeError{
			Code:              wss.CodeVersionMismatch,
			Message:           "Gateway does not advertise the WSS ClientHello contract",
			SupportedVersions: []string{wss.Version},
		}
	}
	connection.SetReadLimit(wss.MaximumHandshakeBytes)
	handshakeCtx, cancelHandshake := context.WithTimeout(dialCtx, forwarder.config.HandshakeTimeout)
	hello := wss.NewClientHello(forwarder.config.ClientVersion, forwarder.config.DeviceID)
	if forwarder.config.TrafficEncryption != nil && !*forwarder.config.TrafficEncryption {
		hello.Capabilities = slices.DeleteFunc(hello.Capabilities, func(value string) bool {
			return value == wss.CapabilityTrafficEncryption
		})
	}
	hello.ProtocolVersions = append([]string(nil), forwarder.config.SupportedVersions...)
	if err := wss.Write(handshakeCtx, connection, hello); err != nil {
		cancelHandshake()
		_ = closeWebSocket(connection, websocket.ClosePolicyViolation, "HANDSHAKE_FAILED")
		return nil, fmt.Errorf("send Gateway WSS ClientHello: %w", err)
	}
	message, err := wss.Read(handshakeCtx, connection)
	cancelHandshake()
	if err != nil {
		_ = closeWebSocket(connection, websocket.ClosePolicyViolation, "HANDSHAKE_FAILED")
		if errors.Is(err, wss.ErrInvalidHandshake) {
			return nil, &HandshakeError{
				Code: wss.CodeInvalidHandshake, Message: "Gateway returned an invalid WSS handshake",
			}
		}
		return nil, fmt.Errorf("read Gateway WSS handshake: %w", err)
	}
	if message.Reject != nil {
		rejection := message.Reject
		_ = closeWebSocket(connection, websocket.ClosePolicyViolation, rejection.Code)
		return nil, &HandshakeError{
			Code: rejection.Code, Message: rejection.Message,
			SupportedVersions: append([]string(nil), rejection.SupportedVersions...),
		}
	}
	serverHello := message.ServerHello
	encryptionEnabled := forwarder.config.TrafficEncryption == nil || *forwarder.config.TrafficEncryption
	if serverHello == nil || !slices.Contains(hello.ProtocolVersions, serverHello.ProtocolVersion) ||
		!slices.Contains(serverHello.Capabilities, "smux.v2") ||
		!slices.Contains(serverHello.Capabilities, "tunnel.open.v2") ||
		!slices.Contains(serverHello.Capabilities, wss.CapabilityTrafficWebSocket) ||
		slices.Contains(serverHello.Capabilities, wss.CapabilityTrafficEncryption) != encryptionEnabled {
		_ = closeWebSocket(connection, websocket.ClosePolicyViolation, "INVALID_HANDSHAKE")
		return nil, errors.New("gateway returned an incompatible WSS ServerHello")
	}
	maximumStreams := min(forwarder.config.MaxStreamsPerConn, serverHello.Limits.MaximumStreamsPerConnection)
	forwarder.mu.Lock()
	forwarder.maxPhysical = min(forwarder.maxPhysical, serverHello.Limits.MaximumConnectionsPerUser)
	forwarder.mu.Unlock()
	streamConn := protocolmux.NewWebSocketConn(forwarder.ctx, connection, websocket.BinaryMessage)
	connection.SetReadLimit(serverHello.Limits.MaximumFrameBytes)
	session, err := smux.Client(streamConn, smuxConfig())
	if err != nil {
		_ = closeWebSocket(connection, websocket.CloseInternalServerErr, "multiplexer setup failed")
		return nil, fmt.Errorf("start Gateway multiplexer: %w", err)
	}
	item := &pooledSession{
		ws: connection, transport: streamConn, session: session, maxStreams: maximumStreams,
		correlationID: correlationID,
	}
	return item, nil
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
	count, maximum := len(forwarder.sessions), forwarder.maxPhysical
	forwarder.mu.Unlock()
	if count >= maximum {
		return nil, errors.New("all Gateway WebSocket sessions are at capacity")
	}
	item, err := forwarder.dial()
	if err != nil {
		return nil, err
	}
	if !forwarder.commitSession(item) {
		_ = item.session.Close()
		_ = closeWebSocket(item.ws, websocket.CloseGoingAway, "client closed during session setup")
		return nil, net.ErrClosed
	}
	return item, nil
}

func (forwarder *Forwarder) commitSession(item *pooledSession) bool {
	forwarder.mu.Lock()
	defer forwarder.mu.Unlock()
	if forwarder.closed {
		return false
	}
	forwarder.sessions = append(forwarder.sessions, item)
	forwarder.wg.Go(func() {
		forwarder.keepAlive(item)
	})
	return true
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
	ctx := middleware.WithID(context.Background(), target.correlationID)
	forwarder.logger.InfoContext(
		ctx, "Gateway WebSocket session closed",
		"operation", "gateway.websocket.session",
		"outcome", "closed",
		"correlation_id", target.correlationID,
		"session_id", forwarder.config.SessionID,
		"session_generation", forwarder.config.SessionGeneration,
	)
	_ = target.session.Close()
	_ = closeWebSocket(target.ws, websocket.CloseGoingAway, "session replaced")
}

func (forwarder *Forwarder) keepAlive(item *pooledSession) {
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
			err := item.transport.Ping(ctx)
			cancel()
			if err != nil {
				forwarder.discard(item)
				return
			}
		}
	}
}
