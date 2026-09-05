package socksbridge

import (
	"context"
	"fmt"
	"net"
	"strconv"
)

func (s *Server) dial(ctx context.Context, network, address string) (net.Conn, error) {
	host, rawPort, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("split SOCKS destination: %w", err)
	}
	value, err := strconv.ParseUint(rawPort, 10, 16)
	if err != nil {
		return nil, fmt.Errorf("parse SOCKS destination port: %w", err)
	}
	port := uint16(value)
	if network == "udp" {
		s.hostMu.RLock()
		hostUDP := s.HostUDP
		s.hostMu.RUnlock()
		if hostUDP != nil {
			if dial, ok := hostUDP(host, port); ok && dial != nil {
				return dial(ctx)
			}
		}
	}
	s.dialerMu.RLock()
	forwardDialer := s.ForwardDialer
	s.dialerMu.RUnlock()
	if forwardDialer == nil {
		return nil, fmt.Errorf("v3 forward transport is unavailable")
	}
	s.logf("%s connect %s", network, address)
	connection, err := forwardDialer.DialContext(ctx, network, address)
	if err != nil {
		s.logf("%s connect %s failed: %v", network, address, err)
		return nil, fmt.Errorf("connect v3 forward transport: %w", err)
	}
	s.logf("%s connected %s", network, address)
	return connection, nil
}
