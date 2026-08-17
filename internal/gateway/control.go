package gateway

import (
	"errors"
	"net"

	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
)

// handleControl keeps the immutable NetworkSpec authorization active for the
// lifetime of a Data Plane Session. Reverse traffic Tasks use separate logical
// streams on the same authenticated tunnel multiplexer session.
func (s *Server) handleControl(
	client net.Conn,
	token tunnel.SessionToken,
	spec networkspec.Spec,
	networkSpecHash string,
	namespace string,
) {
	defer client.Close()

	s.mu.Lock()
	if existing, ok := s.networks[token]; ok &&
		(existing.hash != networkSpecHash || existing.namespace != namespace) {
		s.mu.Unlock()
		_ = tunnel.WriteStatus(client, errors.New("Session NetworkSpec changed"))
		return
	}
	s.networks[token] = tenantNetwork{spec: spec, hash: networkSpecHash, namespace: namespace}
	s.tenants[token]++
	s.mu.Unlock()
	defer s.removeControl(token)

	if err := tunnel.WriteStatus(client, nil); err != nil {
		return
	}

	// No application messages follow the authorization handshake. A read keeps
	// the authorization alive until the peer closes and rejects obsolete V1
	// control messages as soon as any payload arrives.
	var unexpected [1]byte
	_, _ = client.Read(unexpected[:])
}

func (s *Server) removeControl(token tunnel.SessionToken) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tenants[token] <= 1 {
		delete(s.tenants, token)
		delete(s.networks, token)
		return
	}
	s.tenants[token]--
}
