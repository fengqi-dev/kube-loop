package websocketmux

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/xtaci/smux"

	"github.com/fengqi-dev/kube-loop/internal/protocol/websocket"
	"github.com/fengqi-dev/kube-loop/internal/protocol/wssprotocol"
)

func (forwarder *Forwarder) dial() (*pooledSession, error) {
	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("default HTTP transport %T is unsupported", http.DefaultTransport)
	}
	transport := defaultTransport.Clone()
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
		return nil, errors.New("gateway WebSocket token source returned an invalid token")
	}
	header := make(http.Header)
	header.Set("Authorization", "Bearer "+token)
	connection, response, err := websocket.Dial(dialCtx, forwarder.config.URL, &websocket.DialOptions{
		HTTPClient: httpClient, HTTPHeader: header, Subprotocols: []string{Subprotocol},
	})
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
		_ = connection.Close(websocket.StatusPolicyViolation, "subprotocol required")
		return nil, fmt.Errorf(
			"gateway did not negotiate multiplexing subprotocol %q (got %q)",
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
		return nil, errors.New("gateway returned an incompatible WSS ServerHello")
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
