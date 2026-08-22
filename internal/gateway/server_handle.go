package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
)

func (s *Server) handle(ctx context.Context, client net.Conn, required requiredAuthorization) {
	_ = client.SetReadDeadline(time.Now().Add(15 * time.Second))
	header, err := tunnel.ReadSessionHeader(client)
	if err != nil {
		_ = client.Close()
		if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			s.log(required.requestID, "Gateway tunnel handshake rejected", "remote", client.RemoteAddr(), "error", err)
		}
		return
	}
	_ = client.SetReadDeadline(time.Time{})
	if header.Token != required.token {
		s.log(required.requestID, "Gateway tunnel handshake rejected", "reason", "ticket_mismatch")
		_ = tunnel.WriteStatus(client, errors.New("gateway session does not match RelayTicket"))
		_ = client.Close()
		return
	}

	switch header.Command {
	case tunnel.CommandTCP, tunnel.CommandUDP:
		s.handleOutbound(ctx, client, header, &required)
	case tunnel.CommandControl:
		spec, readErr := tunnel.ReadAuthorizedControlSpec(client)
		if readErr != nil {
			s.log(
				required.requestID,
				"Gateway control stream rejected",
				"reason",
				"invalid_network_spec",
				"error",
				readErr,
			)
			_ = tunnel.WriteStatus(client, errors.New("authorized NetworkSpec is invalid"))
			_ = client.Close()
			return
		}
		hash, hashErr := networkspec.Hash(spec)
		if hashErr != nil || hash != required.networkSpecHash {
			s.log(
				required.requestID,
				"Gateway control stream rejected",
				"reason",
				"network_spec_mismatch",
				"error",
				hashErr,
			)
			_ = tunnel.WriteStatus(client, errors.New("NetworkSpec does not match RelayTicket"))
			_ = client.Close()
			return
		}
		s.handleControl(client, header.Token, spec, hash, required.namespace)
	case tunnel.CommandTraffic:
		request, readErr := tunnel.ReadTrafficOpenBody(client)
		if readErr != nil {
			s.log(required.requestID, "Gateway traffic stream rejected", "reason", "invalid_request", "error", readErr)
			_ = tunnel.WriteStatus(client, errors.New("traffic request is invalid"))
			_ = client.Close()
			return
		}
		_, authorized, authorizationErr := s.authorizedNetwork(header.Token, &required)
		if authorizationErr != nil || !authorized {
			if authorizationErr == nil {
				authorizationErr = errors.New("gateway NetworkSpec authorization is not active")
			}
			s.log(
				required.requestID,
				"Gateway traffic stream rejected",
				"reason",
				"authorization",
				"error",
				authorizationErr,
			)
			_ = tunnel.WriteStatus(client, authorizationErr)
			_ = client.Close()
			return
		}
		identity := required.identity
		identity.Groups = slices.Clone(required.identity.Groups)
		if identity.Validate() != nil {
			s.log(required.requestID, "Gateway traffic stream rejected", "reason", "invalid_identity")
			_ = tunnel.WriteStatus(client, errors.New("traffic identity is invalid"))
			_ = client.Close()
			return
		}
		s.mu.Lock()
		traffic := s.traffic
		s.mu.Unlock()
		if traffic == nil {
			_ = tunnel.WriteStatus(client, errors.New("gateway traffic handler is unavailable"))
			_ = client.Close()
			return
		}
		traffic.ServeTraffic(ctx, client, identity, request)
	default:
		s.log(required.requestID, "Gateway tunnel command rejected", "command", header.Command)
		_ = tunnel.WriteStatus(client, fmt.Errorf("unsupported command %d", header.Command))
		_ = client.Close()
	}
}
