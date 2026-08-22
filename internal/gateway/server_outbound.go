package gateway

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
)

func (s *Server) handleOutbound(
	ctx context.Context,
	client net.Conn,
	header tunnel.SessionHeader,
	required *requiredAuthorization,
) {
	defer func() { _ = client.Close() }()
	spec, authorized, authorizationErr := s.authorizedNetwork(header.Token, required)
	if authorizationErr != nil {
		_ = tunnel.WriteStatus(client, authorizationErr)
		s.log(required.requestID, "Gateway tunnel open rejected", "reason", "authorization", "error", authorizationErr)
		return
	}
	request, err := tunnel.ReadOpenBody(client, header.Command)
	if err != nil {
		s.log(required.requestID, "Gateway tunnel open rejected", "remote", client.RemoteAddr(), "error", err)
		return
	}

	ctx, cancel := context.WithTimeout(ctx, s.DialTimeout)
	defer cancel()
	var targetAddress string
	if authorized {
		targetAddress, err = s.resolveAuthorized(ctx, request.Host, request.Port, spec)
	} else {
		targetAddress, err = resolvePrivate(ctx, request.Host, request.Port)
	}
	if err != nil {
		_ = tunnel.WriteStatus(client, err)
		s.log(required.requestID, "Gateway target denied", "target", request.Address(), "error", err)
		return
	}
	network := "tcp"
	if request.Command == tunnel.CommandUDP {
		network = "udp"
	}
	dialer := s.Dialer
	if dialer == nil {
		dialer = &net.Dialer{}
	}
	target, err := dialer.DialContext(ctx, network, targetAddress)
	if err != nil {
		_ = tunnel.WriteStatus(client, fmt.Errorf("dial target: %w", err))
		s.log(
			required.requestID, "Gateway target connection failed",
			"network", network, "target", targetAddress, "error", err,
		)
		return
	}
	defer func() { _ = target.Close() }()
	if err := tunnel.WriteStatus(client, nil); err != nil {
		return
	}

	if request.Command == tunnel.CommandUDP {
		s.relayUDP(client, target)
		return
	}
	relayTCP(client, target)
}

func (s *Server) authorizedNetwork(
	token tunnel.SessionToken,
	required *requiredAuthorization,
) (networkspec.Spec, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tenants[token] <= 0 {
		return networkspec.Spec{}, false, errors.New("gateway session is not active")
	}
	if required == nil {
		return networkspec.Spec{}, false, nil
	}
	network, ok := s.networks[token]
	if !ok || network.hash != required.networkSpecHash || network.namespace != required.namespace {
		return networkspec.Spec{}, false, errors.New("gateway NetworkSpec authorization is not active")
	}
	return network.spec, true, nil
}
