package mcp

import (
	"errors"
	"fmt"
)

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
