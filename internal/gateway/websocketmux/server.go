package websocketmux

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"log"
	"net"
	"net/http"
	"strings"

	"github.com/coder/websocket"
	"github.com/xtaci/smux"
)

type ServerConfig struct {
	Token                string
	MaxSessions          int
	MaxStreamsPerSession int
	Logger               *log.Logger
	Handle               func(net.Conn)
}

type Handler struct {
	config ServerConfig
	limit  chan struct{}
}

func NewHandler(config ServerConfig) (*Handler, error) {
	if config.Token == "" {
		return nil, errors.New("Gateway WebSocket token is required")
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
	return &Handler{config: config, limit: make(chan struct{}, config.MaxSessions)}, nil
}

func (h *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		h.log("WebSocket request rejected: remote=%s method=%s path=%s status=%d reason=method", request.RemoteAddr, request.Method, request.URL.Path, http.StatusMethodNotAllowed)
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.authorized(request) {
		h.log("WebSocket request rejected: remote=%s method=%s path=%s status=%d reason=authentication", request.RemoteAddr, request.Method, request.URL.Path, http.StatusUnauthorized)
		writer.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(writer, "unauthorized", http.StatusUnauthorized)
		return
	}
	select {
	case h.limit <- struct{}{}:
		defer func() { <-h.limit }()
	default:
		h.log("WebSocket request rejected: remote=%s path=%s status=%d reason=session_limit", request.RemoteAddr, request.URL.Path, http.StatusServiceUnavailable)
		http.Error(writer, "Gateway session limit reached", http.StatusServiceUnavailable)
		return
	}
	connection, err := websocket.Accept(writer, request, &websocket.AcceptOptions{
		Subprotocols: []string{Subprotocol},
	})
	if err != nil {
		h.log("WebSocket upgrade failed: remote=%s path=%s error=%v", request.RemoteAddr, request.URL.Path, err)
		return
	}
	defer connection.CloseNow()
	if connection.Subprotocol() != Subprotocol {
		h.log("WebSocket request rejected: remote=%s path=%s reason=subprotocol", request.RemoteAddr, request.URL.Path)
		_ = connection.Close(websocket.StatusPolicyViolation, "subprotocol required")
		return
	}
	h.log("WebSocket session opened: remote=%s path=%s active_sessions=%d subprotocol=%s", request.RemoteAddr, request.URL.Path, len(h.limit), connection.Subprotocol())
	streamConn := websocket.NetConn(request.Context(), connection, websocket.MessageBinary)
	connection.SetReadLimit(1024 * 1024)
	session, err := smux.Server(streamConn, smuxConfig())
	if err != nil {
		h.log("WebSocket multiplexer setup failed: remote=%s error=%v", request.RemoteAddr, err)
		return
	}
	defer session.Close()
	streams := make(chan struct{}, h.config.MaxStreamsPerSession)
	for {
		stream, acceptErr := session.AcceptStream()
		if acceptErr != nil {
			h.log("WebSocket session closed: remote=%s active_sessions=%d error=%v", request.RemoteAddr, len(h.limit)-1, acceptErr)
			return
		}
		select {
		case streams <- struct{}{}:
			go func() {
				defer func() { <-streams }()
				h.config.Handle(stream)
			}()
		default:
			h.log("WebSocket stream rejected: remote=%s reason=stream_limit max_streams=%d", request.RemoteAddr, h.config.MaxStreamsPerSession)
			_ = stream.Close()
		}
	}
}

func (h *Handler) authorized(request *http.Request) bool {
	value := strings.TrimSpace(request.Header.Get("Authorization"))
	const prefix = "Bearer "
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	provided := sha256.Sum256([]byte(strings.TrimSpace(strings.TrimPrefix(value, prefix))))
	expected := sha256.Sum256([]byte(h.config.Token))
	return subtle.ConstantTimeCompare(provided[:], expected[:]) == 1
}

func (h *Handler) log(format string, values ...any) {
	if h.config.Logger != nil {
		h.config.Logger.Printf(format, values...)
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
