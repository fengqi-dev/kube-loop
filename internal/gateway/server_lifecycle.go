package gateway

import (
	"context"
	"net"
)

func (s *Server) trackConnection(connection net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.draining {
		return false
	}
	s.connections[connection] = struct{}{}
	s.connectionsWG.Add(1)
	return true
}

func (s *Server) untrackConnection(connection net.Conn) {
	s.mu.Lock()
	if _, exists := s.connections[connection]; !exists {
		s.mu.Unlock()
		return
	}
	delete(s.connections, connection)
	s.mu.Unlock()
	s.connectionsWG.Done()
}

// BeginDrain prevents new logical Gateway connections while allowing existing
// streams to finish until Drain's context expires.
func (s *Server) BeginDrain() {
	s.mu.Lock()
	s.draining = true
	s.mu.Unlock()
}

func (s *Server) Draining() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.draining
}

func (s *Server) ActiveConnections() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.connections)
}

// Drain waits for active logical connections. When the deadline expires it
// closes them so the process cannot retain stale relays indefinitely.
func (s *Server) Drain(ctx context.Context) error {
	s.BeginDrain()
	done := make(chan struct{})
	go func() {
		s.connectionsWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		s.closeActiveConnections()
		return ctx.Err()
	}
}

func (s *Server) closeActiveConnections() {
	s.mu.Lock()
	connections := make([]net.Conn, 0, len(s.connections))
	for connection := range s.connections {
		connections = append(connections, connection)
	}
	s.mu.Unlock()
	for _, connection := range connections {
		_ = connection.Close()
	}
}
