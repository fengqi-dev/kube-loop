package websocketmux

import (
	"context"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/xtaci/smux"

	"github.com/fengqi-dev/kube-loop/internal/protocol/websocket"
	shared "github.com/fengqi-dev/kube-loop/internal/protocol/websocketmux"
	"github.com/fengqi-dev/kube-loop/internal/protocol/wssprotocol"
)

func (h *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	requestID := writer.Header().Get("X-Request-ID")
	if requestID == "" {
		requestID = request.Header.Get("X-Request-ID")
	}
	if h.draining.Load() {
		h.logf(request.Context(),
			requestID, "WebSocket request rejected: remote=%s method=%s path=%s status=%d reason=draining",
			request.RemoteAddr, request.Method, request.URL.Path, http.StatusServiceUnavailable,
		)
		writer.Header().Set("Retry-After", "5")
		http.Error(writer, "Gateway is draining", http.StatusServiceUnavailable)
		return
	}
	if request.Method != http.MethodGet {
		h.logf(request.Context(),
			requestID, "WebSocket request rejected: remote=%s method=%s path=%s status=%d reason=method",
			request.RemoteAddr, request.Method, request.URL.Path, http.StatusMethodNotAllowed,
		)
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	identity, err := h.config.Authenticator.Authenticate(request)
	if err != nil || identity.IdentityID == "" || identity.DeviceID == "" || identity.SessionID == "" ||
		identity.SessionGeneration == 0 || !identity.ExpiresAt.After(time.Now()) {
		h.logf(request.Context(),
			requestID, "WebSocket request rejected: remote=%s method=%s path=%s status=%d reason=authentication",
			request.RemoteAddr, request.Method, request.URL.Path, http.StatusUnauthorized,
		)
		writer.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(writer, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !h.acquireGeneration(identity) {
		h.logf(request.Context(),
			requestID, "WebSocket request rejected: remote=%s method=%s path=%s status=%d reason=stale_generation",
			request.RemoteAddr, request.Method, request.URL.Path, http.StatusUnauthorized,
		)
		writer.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(writer, "unauthorized", http.StatusUnauthorized)
		return
	}
	identity.RequestID = requestID
	identity.Groups = slices.Clone(identity.Groups)
	defer h.releaseGeneration(identity)
	select {
	case h.limit <- struct{}{}:
		defer func() { <-h.limit }()
	default:
		h.logf(request.Context(),
			requestID, "WebSocket request rejected: remote=%s path=%s status=%d reason=session_limit",
			request.RemoteAddr, request.URL.Path, http.StatusServiceUnavailable,
		)
		http.Error(writer, "Gateway session limit reached", http.StatusServiceUnavailable)
		return
	}
	writer.Header().Set(wssprotocol.VersionHeader, wssprotocol.Version)
	connection, err := websocket.Accept(writer, request, &websocket.AcceptOptions{
		Subprotocols: []string{Subprotocol},
	})
	if err != nil {
		h.logf(request.Context(),
			requestID,
			"WebSocket upgrade failed: remote=%s path=%s error=%v",
			request.RemoteAddr,
			request.URL.Path,
			err,
		)
		return
	}
	defer func() { _ = connection.CloseNow() }()
	if connection.Subprotocol() != Subprotocol {
		h.logf(request.Context(),
			requestID,
			"WebSocket request rejected: remote=%s path=%s reason=subprotocol requested=%q negotiated=%q",
			request.RemoteAddr,
			request.URL.Path,
			request.Header.Get("Sec-WebSocket-Protocol"),
			connection.Subprotocol(),
		)
		_ = connection.Close(websocket.StatusPolicyViolation, "subprotocol required")
		return
	}
	connection.SetReadLimit(wssprotocol.MaximumHandshakeBytes)
	handshakeContext, cancelHandshake := context.WithTimeout(request.Context(), h.config.HandshakeTimeout)
	message, handshakeErr := wssprotocol.Read(handshakeContext, connection)
	if handshakeErr != nil || message.ClientHello == nil {
		cancelHandshake()
		h.reject(
			request.Context(),
			requestID,
			connection,
			wssprotocol.NewReject(wssprotocol.CodeInvalidHandshake, "ClientHello is invalid"),
		)
		return
	}
	hello := *message.ClientHello
	selectedVersion, versionErr := wssprotocol.Negotiate(hello.ProtocolVersions, h.config.SupportedVersions)
	if versionErr != nil {
		cancelHandshake()
		h.reject(request.Context(), requestID, connection, wssprotocol.NewReject(
			wssprotocol.CodeVersionMismatch, "No compatible WSS protocol version", h.config.SupportedVersions...,
		))
		return
	}
	if err := wssprotocol.CheckClientVersion(hello.ClientVersion, h.config.MinClientVersion); err != nil {
		cancelHandshake()
		h.reject(request.Context(), requestID, connection, wssprotocol.NewReject(
			wssprotocol.CodeClientVersionUnsupported, "Client version is not supported",
		))
		return
	}
	if hello.DeviceID != identity.DeviceID {
		cancelHandshake()
		h.reject(
			request.Context(),
			requestID, connection,
			wssprotocol.NewReject(wssprotocol.CodeDeviceMismatch, "Device does not match RelayTicket"),
		)
		return
	}
	if !slices.Contains(hello.Capabilities, "smux.v2") || !slices.Contains(hello.Capabilities, "tunnel.open.v2") ||
		!slices.Contains(hello.Capabilities, wssprotocol.CapabilityTrafficWebSocket) {
		cancelHandshake()
		h.reject(
			request.Context(),
			requestID, connection,
			wssprotocol.NewReject(wssprotocol.CodeInvalidHandshake, "Required capabilities are missing"),
		)
		return
	}
	if !h.acquireUser(identity) {
		cancelHandshake()
		h.reject(
			request.Context(),
			requestID, connection,
			wssprotocol.NewReject(wssprotocol.CodeUserCapacityExceeded, "Per-user connection limit reached"),
		)
		return
	}
	defer h.releaseUser(identity)
	serverHello := wssprotocol.NewServerHello(h.config.ServerVersion, wssprotocol.Limits{
		MaximumFrameBytes: h.config.MaxFrameBytes, MaximumStreamFrameBytes: shared.MaximumStreamFrameBytes,
		MaximumStreamsPerConnection: h.config.MaxStreamsPerSession,
		MaximumPhysicalConnections:  h.config.MaxSessions, MaximumConnectionsPerUser: h.config.MaxSessionsPerUser,
		StreamIdleTimeoutMillis: h.config.StreamIdleTimeout.Milliseconds(),
	})
	serverHello.ProtocolVersion = selectedVersion
	if err := wssprotocol.Write(handshakeContext, connection, serverHello); err != nil {
		cancelHandshake()
		h.logf(
			request.Context(), requestID,
			"WebSocket handshake response failed: remote=%s error=%v",
			request.RemoteAddr, err,
		)
		_ = connection.Close(websocket.StatusInternalError, "HANDSHAKE_FAILED")
		return
	}
	cancelHandshake()
	connection.SetReadLimit(h.config.MaxFrameBytes)
	h.logSession(
		request.Context(), "Gateway WebSocket session opened",
		"gateway.websocket.session", "opened", identity,
		"active_sessions", len(h.limit), "subprotocol", connection.Subprotocol(),
	)
	// RelayTicket expiry is an admission boundary for the authenticated WSS
	// handshake, not a lifetime limit for accepted logical streams. Established
	// sessions remain governed by generation fencing, shutdown and explicit close.
	streamConn := websocket.NetConn(request.Context(), connection, websocket.MessageBinary)
	session, err := smux.Server(streamConn, smuxConfig())
	if err != nil {
		h.logf(
			request.Context(), requestID,
			"WebSocket multiplexer setup failed: remote=%s error=%v",
			request.RemoteAddr, err,
		)
		return
	}
	var streamHandlers sync.WaitGroup
	defer streamHandlers.Wait()
	defer func() { _ = session.Close() }()
	streams := make(chan struct{}, h.config.MaxStreamsPerSession)
	for {
		stream, acceptErr := session.AcceptStream()
		if acceptErr != nil {
			h.logSession(
				request.Context(), "Gateway WebSocket session closed",
				"gateway.websocket.session", "closed", identity,
				"active_sessions", len(h.limit)-1, "error", acceptErr,
			)
			return
		}
		select {
		case streams <- struct{}{}:
			if !h.generationCurrent(identity) {
				<-streams
				h.logf(request.Context(),
					requestID, "WebSocket stream rejected: remote=%s reason=stale_generation generation=%d",
					request.RemoteAddr, identity.SessionGeneration,
				)
				_ = stream.Close()
				continue
			}
			streamIdentity := identity
			streamIdentity.Groups = slices.Clone(identity.Groups)
			streamHandlers.Go(func() {
				defer func() { <-streams }()
				startedAt := time.Now()
				h.logSession(
					request.Context(), "Gateway logical stream opened",
					"gateway.tunnel.stream", "opened", streamIdentity, "stream_id", stream.ID(),
				)
				defer func() {
					h.logSession(
						request.Context(), "Gateway logical stream closed",
						"gateway.tunnel.stream", "closed", streamIdentity, "stream_id", stream.ID(),
						"duration_ms", time.Since(startedAt).Milliseconds(),
					)
				}()
				h.config.Handle(
					request.Context(), streamIdentity,
					shared.NewStreamConnWithIdleTimeout(stream, h.config.StreamIdleTimeout),
				)
			})
		default:
			h.logf(request.Context(),
				requestID, "WebSocket stream rejected: remote=%s reason=stream_limit max_streams=%d",
				request.RemoteAddr, h.config.MaxStreamsPerSession,
			)
			_ = stream.Close()
		}
	}
}
