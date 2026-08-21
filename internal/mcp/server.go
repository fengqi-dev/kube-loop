package mcp

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const pathPrefix = "/mcp"

// Status is the Settings / UI view of the embedded MCP server.
type Status struct {
	Enabled      bool   `json:"enabled"`
	Listening    bool   `json:"listening"`
	URL          string `json:"url,omitempty"`
	Port         int    `json:"port"`
	TokenEnabled bool   `json:"tokenEnabled"`
	Token        string `json:"token,omitempty"`
	Error        string `json:"error,omitempty"`
}

// Server hosts a Streamable HTTP MCP endpoint on 127.0.0.1.
type Server struct {
	backend Backend
	version string

	mu           sync.Mutex
	enabled      bool
	port         int
	tokenEnabled bool
	token        string
	httpServer   *http.Server
	listener     net.Listener
	lastErr      string
	onError      func(error)
}

// NewServer builds an MCP server bound to backend. Call Start after configuring
// enabled/port/token (typically from store.MCPConfig).
func NewServer(backend Backend, version string) *Server {
	if version == "" {
		version = "dev"
	}
	return &Server{
		backend: backend,
		version: version,
		port:    DefaultPort,
	}
}

// SetErrorHandler receives unexpected asynchronous listener failures.
func (s *Server) SetErrorHandler(handler func(error)) {
	s.mu.Lock()
	s.onError = handler
	s.mu.Unlock()
}

// Configure updates persisted settings without starting or stopping.
// Use Apply to reconcile the listener with Enabled.
func (s *Server) Configure(cfg Config) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enabled = cfg.Enabled
	s.port = cfg.Port
	if s.port <= 0 {
		s.port = DefaultPort
	}
	s.tokenEnabled = cfg.TokenEnabled
	s.token = cfg.Token
}

// Config returns the current configuration snapshot.
func (s *Server) Config() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Config{
		Enabled:      s.enabled,
		Port:         s.port,
		TokenEnabled: s.tokenEnabled,
		Token:        s.token,
	}
}

// Status returns runtime + config for the UI.
func (s *Server) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{
		Enabled:      s.enabled,
		Listening:    s.listener != nil,
		Port:         s.port,
		TokenEnabled: s.tokenEnabled,
		Error:        s.lastErr,
	}
	if s.tokenEnabled {
		st.Token = s.token
	}
	if st.Port > 0 {
		st.URL = fmt.Sprintf("http://127.0.0.1:%d%s", st.Port, pathPrefix)
	}
	return st
}

// Apply starts or stops the listener to match Enabled and current port/token.
func (s *Server) Apply() error {
	s.mu.Lock()
	enabled := s.enabled
	s.mu.Unlock()
	if !enabled {
		return s.Stop()
	}
	return s.Start()
}

// Start listens on 127.0.0.1:port. Idempotent when already listening on the same port
// (avoids wiping Streamable HTTP sessions that Cursor reconnects with via GET SSE).
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tokenEnabled && s.token == "" {
		return errors.New("mcp token is required when token auth is enabled")
	}
	if s.port <= 0 || s.port > 65535 {
		return fmt.Errorf("invalid mcp port %d", s.port)
	}
	if s.listener != nil {
		addr := s.listener.Addr().String()
		want := fmt.Sprintf("127.0.0.1:%d", s.port)
		if addr == want {
			s.enabled = true
			s.lastErr = ""
			return nil
		}
		if err := s.stopLocked(); err != nil {
			return err
		}
	}

	mcpServer := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    "kubeloop",
		Version: s.version,
	}, nil)
	registerTools(mcpServer, s.backend)

	streamHandler := mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server {
		return mcpServer
	}, nil)

	var mcpHandler http.Handler = streamHandler
	if s.tokenEnabled {
		token := s.token
		mcpHandler = auth.RequireBearerToken(
			func(_ context.Context, got string, _ *http.Request) (*auth.TokenInfo, error) {
				if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
					return nil, auth.ErrInvalidToken
				}
				return &auth.TokenInfo{
					Scopes:     []string{"mcp"},
					Expiration: time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC),
				}, nil
			},
			nil,
		)(
			streamHandler,
		)
	}

	mux := http.NewServeMux()
	mux.Handle(pathPrefix, localhostOnly(mcpHandler))
	mux.Handle(pathPrefix+"/", localhostOnly(mcpHandler))

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", s.port))
	if err != nil {
		s.lastErr = err.Error()
		return fmt.Errorf("listen mcp: %w", err)
	}
	httpServer := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	s.listener = listener
	s.httpServer = httpServer
	s.enabled = true
	s.lastErr = ""

	go func() {
		err := httpServer.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("mcp server: %v", err)
			s.mu.Lock()
			s.lastErr = err.Error()
			s.listener = nil
			s.httpServer = nil
			onError := s.onError
			s.mu.Unlock()
			if onError != nil {
				onError(err)
			}
		}
	}()
	return nil
}

// Stop closes the HTTP listener.
func (s *Server) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enabled = false
	return s.stopLocked()
}

func (s *Server) stopLocked() error {
	httpServer := s.httpServer
	s.httpServer = nil
	s.listener = nil
	if httpServer == nil {
		return nil
	}
	// Use Close, not Shutdown: Streamable HTTP keeps hanging GET/SSE connections
	// that never complete, so Shutdown waits until the context deadline and
	// surfaces "context deadline exceeded" to the UI.
	err := httpServer.Close()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		s.lastErr = err.Error()
		return err
	}
	s.lastErr = ""
	return nil
}

// SetEnabled updates the enabled flag and reconciles the listener.
func (s *Server) SetEnabled(enabled bool) error {
	s.mu.Lock()
	s.enabled = enabled
	s.mu.Unlock()
	return s.Apply()
}

// SetPort updates the listen port and restarts if currently listening on a different port.
func (s *Server) SetPort(port int) error {
	if port <= 0 || port > 65535 {
		return fmt.Errorf("invalid mcp port %d", port)
	}
	s.mu.Lock()
	if s.port == port {
		s.mu.Unlock()
		return nil
	}
	wasListening := s.listener != nil
	s.port = port
	enabled := s.enabled
	s.mu.Unlock()
	if wasListening || enabled {
		_ = s.Stop()
		s.mu.Lock()
		s.enabled = enabled
		s.mu.Unlock()
		if enabled {
			return s.Start()
		}
	}
	return nil
}

// SetTokenEnabled turns Bearer token auth on or off and restarts if listening.
func (s *Server) SetTokenEnabled(enabled bool) error {
	s.mu.Lock()
	wasListening := s.listener != nil
	s.tokenEnabled = enabled
	serverEnabled := s.enabled
	s.mu.Unlock()
	if wasListening {
		_ = s.Stop()
		s.mu.Lock()
		s.enabled = serverEnabled
		s.mu.Unlock()
		if serverEnabled {
			return s.Start()
		}
	}
	return nil
}

// SetToken replaces the bearer token. Restarts the listener so in-flight
// handlers keep using the previous verifier until restart.
func (s *Server) SetToken(token string) error {
	if token == "" {
		return errors.New("mcp token is required")
	}
	s.mu.Lock()
	wasListening := s.listener != nil
	s.token = token
	enabled := s.enabled
	s.mu.Unlock()
	if wasListening {
		_ = s.Stop()
		s.mu.Lock()
		s.enabled = enabled
		s.mu.Unlock()
		if enabled {
			return s.Start()
		}
	}
	return nil
}

func localhostOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.Trim(host, "[]")
		if host != "127.0.0.1" && !strings.EqualFold(host, "localhost") {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			if !isLocalOrigin(origin) {
				http.Error(w, "forbidden origin", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func isLocalOrigin(origin string) bool {
	origin = strings.TrimSpace(origin)
	if origin == "null" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || (parsed.Scheme != transportHTTP && parsed.Scheme != "https") ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return false
	}
	host := parsed.Hostname()
	return strings.EqualFold(host, "localhost") || host == "127.0.0.1" || host == "::1"
}
