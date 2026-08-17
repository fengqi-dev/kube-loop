package websocketmux

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/websocket"
	shared "github.com/fengqi-dev/kube-loop/internal/protocol/websocketmux"
	"github.com/fengqi-dev/kube-loop/internal/protocol/wssprotocol"
	"github.com/xtaci/smux"
)

type Identity struct {
	RequestID         string
	IdentityID        string
	Groups            []string
	DeviceID          string
	SessionID         string
	SessionGeneration uint64
	Namespace         string
	NetworkSpecHash   string
	ExpiresAt         time.Time
}

type Authenticator interface {
	Authenticate(*http.Request) (Identity, error)
}

type AuthenticatorFunc func(*http.Request) (Identity, error)

func (function AuthenticatorFunc) Authenticate(request *http.Request) (Identity, error) {
	return function(request)
}

type ServerConfig struct {
	Authenticator        Authenticator
	MaxSessions          int
	MaxStreamsPerSession int
	MaxSessionsPerUser   int
	MaxFrameBytes        int64
	StreamIdleTimeout    time.Duration
	HandshakeTimeout     time.Duration
	ServerVersion        string
	MinClientVersion     string
	SupportedVersions    []string
	Logger               *slog.Logger
	Handle               func(context.Context, Identity, net.Conn)
}

type Handler struct {
	config       ServerConfig
	limit        chan struct{}
	draining     atomic.Bool
	generationMu sync.Mutex
	generations  map[string]activeGeneration
	userMu       sync.Mutex
	userSessions map[string]int
}

type activeGeneration struct {
	latest   uint64
	sessions int
}

func NewHandler(config ServerConfig) (*Handler, error) {
	if config.Authenticator == nil {
		return nil, errors.New("Gateway WebSocket authenticator is required")
	}
	if config.Handle == nil {
		return nil, errors.New("Gateway stream handler is required")
	}
	if config.MaxSessions <= 0 {
		config.MaxSessions = 256
	}
	if config.MaxStreamsPerSession <= 0 {
		config.MaxStreamsPerSession = defaultMaxStreams
	}
	if config.MaxSessionsPerUser <= 0 {
		config.MaxSessionsPerUser = 8
	}
	if config.MaxSessionsPerUser > config.MaxSessions {
		return nil, errors.New("Gateway per-user session limit exceeds the global limit")
	}
	if config.MaxFrameBytes == 0 {
		config.MaxFrameBytes = 1 << 20
	}
	if config.MaxFrameBytes < wssprotocol.MaximumHandshakeBytes || config.MaxFrameBytes > 16<<20 {
		return nil, errors.New("Gateway WebSocket frame limit is invalid")
	}
	if config.HandshakeTimeout <= 0 {
		config.HandshakeTimeout = wssprotocol.DefaultHandshakeTimeout
	}
	if config.HandshakeTimeout > time.Minute {
		return nil, errors.New("Gateway WSS handshake timeout must not exceed one minute")
	}
	if len(config.SupportedVersions) == 0 {
		config.SupportedVersions = []string{wssprotocol.Version}
	}
	if len(config.SupportedVersions) != 1 || config.SupportedVersions[0] != wssprotocol.Version {
		return nil, errors.New("Gateway WSS protocol versions are invalid")
	}
	if config.StreamIdleTimeout <= 0 {
		config.StreamIdleTimeout = defaultStreamIdleTimeout
	}
	if config.StreamIdleTimeout > maxStreamIdleTimeout {
		return nil, errors.New("Gateway stream idle timeout must not exceed 24 hours")
	}
	return &Handler{
		config: config, limit: make(chan struct{}, config.MaxSessions),
		generations: make(map[string]activeGeneration), userSessions: make(map[string]int),
	}, nil
}

func (h *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	requestID := writer.Header().Get("X-Request-ID")
	if requestID == "" {
		requestID = request.Header.Get("X-Request-ID")
	}
	if h.draining.Load() {
		h.log(requestID, "WebSocket request rejected: remote=%s method=%s path=%s status=%d reason=draining", request.RemoteAddr, request.Method, request.URL.Path, http.StatusServiceUnavailable)
		writer.Header().Set("Retry-After", "5")
		http.Error(writer, "Gateway is draining", http.StatusServiceUnavailable)
		return
	}
	if request.Method != http.MethodGet {
		h.log(requestID, "WebSocket request rejected: remote=%s method=%s path=%s status=%d reason=method", request.RemoteAddr, request.Method, request.URL.Path, http.StatusMethodNotAllowed)
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	identity, err := h.config.Authenticator.Authenticate(request)
	if err != nil || identity.IdentityID == "" || identity.DeviceID == "" || identity.SessionID == "" ||
		identity.SessionGeneration == 0 || !identity.ExpiresAt.After(time.Now()) {
		h.log(requestID, "WebSocket request rejected: remote=%s method=%s path=%s status=%d reason=authentication", request.RemoteAddr, request.Method, request.URL.Path, http.StatusUnauthorized)
		writer.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(writer, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !h.acquireGeneration(identity) {
		h.log(requestID, "WebSocket request rejected: remote=%s method=%s path=%s status=%d reason=stale_generation", request.RemoteAddr, request.Method, request.URL.Path, http.StatusUnauthorized)
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
		h.log(requestID, "WebSocket request rejected: remote=%s path=%s status=%d reason=session_limit", request.RemoteAddr, request.URL.Path, http.StatusServiceUnavailable)
		http.Error(writer, "Gateway session limit reached", http.StatusServiceUnavailable)
		return
	}
	writer.Header().Set(wssprotocol.VersionHeader, wssprotocol.Version)
	connection, err := websocket.Accept(writer, request, &websocket.AcceptOptions{
		Subprotocols: []string{Subprotocol},
	})
	if err != nil {
		h.log(requestID, "WebSocket upgrade failed: remote=%s path=%s error=%v", request.RemoteAddr, request.URL.Path, err)
		return
	}
	defer connection.CloseNow()
	if connection.Subprotocol() != Subprotocol {
		h.log(requestID,
			"WebSocket request rejected: remote=%s path=%s reason=subprotocol requested=%q negotiated=%q",
			request.RemoteAddr, request.URL.Path, request.Header.Get("Sec-WebSocket-Protocol"), connection.Subprotocol(),
		)
		_ = connection.Close(websocket.StatusPolicyViolation, "subprotocol required")
		return
	}
	connection.SetReadLimit(wssprotocol.MaximumHandshakeBytes)
	handshakeContext, cancelHandshake := context.WithTimeout(request.Context(), h.config.HandshakeTimeout)
	message, handshakeErr := wssprotocol.Read(handshakeContext, connection)
	if handshakeErr != nil || message.ClientHello == nil {
		cancelHandshake()
		h.reject(requestID, connection, wssprotocol.NewReject(wssprotocol.CodeInvalidHandshake, "ClientHello is invalid"))
		return
	}
	hello := *message.ClientHello
	selectedVersion, versionErr := wssprotocol.Negotiate(hello.ProtocolVersions, h.config.SupportedVersions)
	if versionErr != nil {
		cancelHandshake()
		h.reject(requestID, connection, wssprotocol.NewReject(
			wssprotocol.CodeVersionMismatch, "No compatible WSS protocol version", h.config.SupportedVersions...,
		))
		return
	}
	if err := wssprotocol.CheckClientVersion(hello.ClientVersion, h.config.MinClientVersion); err != nil {
		cancelHandshake()
		h.reject(requestID, connection, wssprotocol.NewReject(
			wssprotocol.CodeClientVersionUnsupported, "Client version is not supported",
		))
		return
	}
	if hello.DeviceID != identity.DeviceID {
		cancelHandshake()
		h.reject(requestID, connection, wssprotocol.NewReject(wssprotocol.CodeDeviceMismatch, "Device does not match RelayTicket"))
		return
	}
	if !slices.Contains(hello.Capabilities, "smux.v2") || !slices.Contains(hello.Capabilities, "tunnel.open.v2") ||
		!slices.Contains(hello.Capabilities, wssprotocol.CapabilityTrafficWebSocket) {
		cancelHandshake()
		h.reject(requestID, connection, wssprotocol.NewReject(wssprotocol.CodeInvalidHandshake, "Required capabilities are missing"))
		return
	}
	if !h.acquireUser(identity) {
		cancelHandshake()
		h.reject(requestID, connection, wssprotocol.NewReject(wssprotocol.CodeUserCapacityExceeded, "Per-user connection limit reached"))
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
		h.log(requestID, "WebSocket handshake response failed: remote=%s error=%v", request.RemoteAddr, err)
		_ = connection.Close(websocket.StatusInternalError, "HANDSHAKE_FAILED")
		return
	}
	cancelHandshake()
	connection.SetReadLimit(h.config.MaxFrameBytes)
	h.log(requestID, "WebSocket session opened: remote=%s path=%s active_sessions=%d subprotocol=%s", request.RemoteAddr, request.URL.Path, len(h.limit), connection.Subprotocol())
	// RelayTicket expiry is an admission boundary for the authenticated WSS
	// handshake, not a lifetime limit for accepted logical streams. Established
	// sessions remain governed by generation fencing, shutdown and explicit close.
	streamConn := websocket.NetConn(request.Context(), connection, websocket.MessageBinary)
	session, err := smux.Server(streamConn, smuxConfig())
	if err != nil {
		h.log(requestID, "WebSocket multiplexer setup failed: remote=%s error=%v", request.RemoteAddr, err)
		return
	}
	defer session.Close()
	streams := make(chan struct{}, h.config.MaxStreamsPerSession)
	for {
		stream, acceptErr := session.AcceptStream()
		if acceptErr != nil {
			h.log(requestID, "WebSocket session closed: remote=%s active_sessions=%d error=%v", request.RemoteAddr, len(h.limit)-1, acceptErr)
			return
		}
		select {
		case streams <- struct{}{}:
			if !h.generationCurrent(identity) {
				<-streams
				h.log(requestID, "WebSocket stream rejected: remote=%s reason=stale_generation generation=%d", request.RemoteAddr, identity.SessionGeneration)
				_ = stream.Close()
				continue
			}
			streamIdentity := identity
			streamIdentity.Groups = slices.Clone(identity.Groups)
			go func() {
				defer func() { <-streams }()
				h.config.Handle(request.Context(), streamIdentity, shared.NewStreamConnWithIdleTimeout(stream, h.config.StreamIdleTimeout))
			}()
		default:
			h.log(requestID, "WebSocket stream rejected: remote=%s reason=stream_limit max_streams=%d", request.RemoteAddr, h.config.MaxStreamsPerSession)
			_ = stream.Close()
		}
	}
}

func (h *Handler) acquireGeneration(identity Identity) bool {
	h.generationMu.Lock()
	defer h.generationMu.Unlock()
	current := h.generations[identity.SessionID]
	if identity.SessionGeneration < current.latest {
		return false
	}
	if identity.SessionGeneration > current.latest {
		current.latest = identity.SessionGeneration
	}
	current.sessions++
	h.generations[identity.SessionID] = current
	return true
}

func (h *Handler) reject(requestID string, connection *websocket.Conn, rejection wssprotocol.Reject) {
	h.log(requestID, "WebSocket handshake rejected: reason=%s", rejection.Code)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	_ = wssprotocol.Write(ctx, connection, rejection)
	cancel()
	_ = connection.Close(websocket.StatusPolicyViolation, rejection.Code)
}

func (h *Handler) acquireUser(identity Identity) bool {
	key := identity.IdentityID
	h.userMu.Lock()
	defer h.userMu.Unlock()
	if h.userSessions[key] >= h.config.MaxSessionsPerUser {
		return false
	}
	h.userSessions[key]++
	return true
}

func (h *Handler) releaseUser(identity Identity) {
	key := identity.IdentityID
	h.userMu.Lock()
	defer h.userMu.Unlock()
	if h.userSessions[key] <= 1 {
		delete(h.userSessions, key)
		return
	}
	h.userSessions[key]--
}

func (h *Handler) releaseGeneration(identity Identity) {
	h.generationMu.Lock()
	defer h.generationMu.Unlock()
	current := h.generations[identity.SessionID]
	current.sessions--
	if current.sessions <= 0 {
		delete(h.generations, identity.SessionID)
		return
	}
	h.generations[identity.SessionID] = current
}

func (h *Handler) generationCurrent(identity Identity) bool {
	h.generationMu.Lock()
	defer h.generationMu.Unlock()
	return identity.SessionGeneration >= h.generations[identity.SessionID].latest
}

func (h *Handler) BeginDrain() {
	h.draining.Store(true)
}

func (h *Handler) Draining() bool {
	return h.draining.Load()
}

func (h *Handler) ActiveSessions() int {
	return len(h.limit)
}

func (h *Handler) log(requestID, format string, values ...any) {
	if h.config.Logger != nil {
		h.config.Logger.Info(fmt.Sprintf(format, values...), "request_id", requestID)
	}
}

func Serve(ctx context.Context, listener net.Listener, handler http.Handler) error {
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: defaultKeepAliveInterval,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = server.Close()
		case <-done:
		}
	}()
	err := server.Serve(listener)
	close(done)
	if errors.Is(err, http.ErrServerClosed) && ctx.Err() != nil {
		return nil
	}
	return err
}
