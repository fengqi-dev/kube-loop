package websocketmux

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/xtaci/smux"

	"github.com/fengqi-dev/kube-loop/internal/correlation"
	protocolmux "github.com/fengqi-dev/kube-loop/internal/protocol/websocketmux"
	"github.com/fengqi-dev/kube-loop/internal/protocol/wssprotocol"
)

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
	dialCtx, correlationID := correlation.Ensure(dialCtx)
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
	header.Set(correlation.Header, correlationID)
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
	if response == nil || response.Header.Get(wssprotocol.VersionHeader) != wssprotocol.Version {
		_ = closeWebSocket(connection, websocket.ClosePolicyViolation, wssprotocol.CodeVersionMismatch)
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
		_ = closeWebSocket(connection, websocket.ClosePolicyViolation, "HANDSHAKE_FAILED")
		return nil, fmt.Errorf("send Gateway WSS ClientHello: %w", err)
	}
	message, err := wssprotocol.Read(handshakeCtx, connection)
	cancelHandshake()
	if err != nil {
		_ = closeWebSocket(connection, websocket.ClosePolicyViolation, "HANDSHAKE_FAILED")
		if errors.Is(err, wssprotocol.ErrInvalidHandshake) {
			return nil, &HandshakeError{
				Code: wssprotocol.CodeInvalidHandshake, Message: "Gateway returned an invalid WSS handshake",
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
	if serverHello == nil || !slices.Contains(hello.ProtocolVersions, serverHello.ProtocolVersion) ||
		!slices.Contains(serverHello.Capabilities, "smux.v2") ||
		!slices.Contains(serverHello.Capabilities, "tunnel.open.v2") ||
		!slices.Contains(serverHello.Capabilities, wssprotocol.CapabilityTrafficWebSocket) {
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
