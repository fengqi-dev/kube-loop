package mcp

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

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

	// The listener is closed explicitly by Stop and the Serve goroutine.
	listener, err := net.Listen( //nolint:noctx // Server lifecycle is not owned by a caller context.
		"tcp",
		fmt.Sprintf("127.0.0.1:%d", s.port),
	)
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
