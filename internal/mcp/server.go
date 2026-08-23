package mcp

import (
	"fmt"
	"net"
	"net/http"
	"sync"
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
