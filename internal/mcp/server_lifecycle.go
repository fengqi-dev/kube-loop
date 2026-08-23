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
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	return s.apply()
}

func (s *Server) apply() error {
	s.mu.Lock()
	enabled := s.enabled
	s.mu.Unlock()
	if !enabled {
		return s.stop()
	}
	return s.start()
}

// Start listens on 127.0.0.1:port. Idempotent when already listening on the same port
// (avoids wiping Streamable HTTP sessions that Cursor reconnects with via GET SSE).
func (s *Server) Start() error {
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	return s.start()
}

func (s *Server) start() error {
	s.mu.Lock()
	if s.tokenEnabled && s.token == "" {
		s.mu.Unlock()
		return errors.New("mcp token is required when token auth is enabled")
	}
	if s.port <= 0 || s.port > 65535 {
		s.mu.Unlock()
		return fmt.Errorf("invalid mcp port %d", s.port)
	}
	if s.listener != nil {
		addr := s.listener.Addr().String()
		want := fmt.Sprintf("127.0.0.1:%d", s.port)
		if addr == want {
			s.enabled = true
			s.lastErr = ""
			s.mu.Unlock()
			return nil
		}
	}
	replaceServing := s.listener != nil || s.serveDone != nil
	s.mu.Unlock()
	if replaceServing {
		if err := s.stopServing(); err != nil {
			return err
		}
	}
	s.mu.Lock()
	port := s.port
	tokenEnabled := s.tokenEnabled
	token := s.token
	s.mu.Unlock()

	mcpServer := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    "kubeloop",
		Version: s.version,
	}, nil)
	registerTools(mcpServer, s.backend)

	streamHandler := mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server {
		return mcpServer
	}, nil)

	var mcpHandler http.Handler = streamHandler
	if tokenEnabled {
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
		fmt.Sprintf("127.0.0.1:%d", port),
	)
	if err != nil {
		s.mu.Lock()
		s.lastErr = err.Error()
		s.mu.Unlock()
		return fmt.Errorf("listen mcp: %w", err)
	}
	httpServer := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	done := make(chan struct{})
	s.mu.Lock()
	s.listener, s.httpServer, s.serveDone = listener, httpServer, done
	s.enabled, s.lastErr = true, ""
	s.mu.Unlock()

	go func() {
		defer close(done)
		err := httpServer.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("mcp server: %v", err)
			s.mu.Lock()
			current := s.httpServer == httpServer
			if current {
				s.lastErr = err.Error()
				s.listener = nil
				s.httpServer = nil
			}
			onError := s.onError
			s.mu.Unlock()
			if current && onError != nil {
				onError(err)
			}
		}
	}()
	return nil
}

// Stop closes the HTTP listener.
func (s *Server) Stop() error {
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	return s.stop()
}

func (s *Server) stop() error {
	s.mu.Lock()
	s.enabled = false
	s.mu.Unlock()
	return s.stopServing()
}

func (s *Server) stopServing() error {
	s.mu.Lock()
	httpServer := s.httpServer
	done := s.serveDone
	s.httpServer = nil
	s.listener = nil
	s.serveDone = nil
	s.mu.Unlock()
	if httpServer == nil {
		if done != nil {
			<-done
		}
		return nil
	}
	// Use Close, not Shutdown: Streamable HTTP keeps hanging GET/SSE connections
	// that never complete, so Shutdown waits until the context deadline and
	// surfaces "context deadline exceeded" to the UI.
	err := httpServer.Close()
	if done != nil {
		<-done
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		s.mu.Lock()
		s.lastErr = err.Error()
		s.mu.Unlock()
		return err
	}
	s.mu.Lock()
	s.lastErr = ""
	s.mu.Unlock()
	return nil
}
