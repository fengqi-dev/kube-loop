package socksbridge

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
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
	if network != "udp" {
		return s.openGateway(ctx, tunnel.CommandTCP, host, port)
	}
	s.hostMu.RLock()
	hostUDP := s.HostUDP
	s.hostMu.RUnlock()
	if hostUDP != nil {
		if dial, ok := hostUDP(host, port); ok && dial != nil {
			return dial(ctx)
		}
	}
	connection, err := s.openGateway(ctx, tunnel.CommandUDP, host, port)
	if err != nil {
		return nil, err
	}
	return newFramedConn(connection), nil
}

func (s *Server) openGateway(
	ctx context.Context,
	command byte,
	host string,
	port uint16,
) (net.Conn, error) {
	protocol := "TCP"
	if command == tunnel.CommandUDP {
		protocol = "UDP"
	}
	destination := net.JoinHostPort(host, strconv.Itoa(int(port)))
	s.logf("%s connect %s", protocol, destination)
	timeout := s.DialTimeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	s.gatewayMu.RLock()
	gatewayAddress := s.GatewayAddress
	sessionToken := s.SessionToken
	s.gatewayMu.RUnlock()
	connection, err := (&net.Dialer{Timeout: timeout}).DialContext(ctx, "tcp", gatewayAddress)
	if err != nil {
		s.logf("%s connect %s failed: %v", protocol, destination, err)
		return nil, fmt.Errorf("connect gateway: %w", err)
	}
	request := tunnel.OpenRequest{
		Command: command, Host: host, Port: port,
	}
	if err := tunnel.WriteOpen(connection, request, sessionToken); err != nil {
		closeErr := connection.Close()
		s.logf("%s connect %s failed: %v", protocol, destination, err)
		return nil, errors.Join(err, closeErr)
	}
	if err := tunnel.ReadStatus(connection); err != nil {
		closeErr := connection.Close()
		s.logf("%s connect %s failed: %v", protocol, destination, err)
		return nil, errors.Join(err, closeErr)
	}
	s.logf("%s connected %s", protocol, destination)
	return connection, nil
}
