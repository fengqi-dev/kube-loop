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
	"github.com/fengqi-dev/kube-loop/internal/utils"
)

func (s *Server) handle(ctx context.Context, client net.Conn, required requiredAuthorization) {
	_ = client.SetReadDeadline(time.Now().Add(15 * time.Second))
	header, err := tunnel.ReadSessionHeader(client)
	if err != nil {
		_ = client.Close()
		if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			s.log(
				ctx, required.requestID, "Gateway tunnel handshake rejected",
				"remote", client.RemoteAddr(), "error", err,
			)
		}
		return
	}
	_ = client.SetReadDeadline(time.Time{})
	if header.Token != required.token {
		s.log(ctx, required.requestID, "Gateway tunnel handshake rejected", "reason", "ticket_mismatch")
		_ = tunnel.WriteStatus(client, errors.New("gateway session does not match RelayTicket"))
		_ = client.Close()
		return
	}

	switch header.Command {
	case tunnel.CommandControl:
		spec, readErr := tunnel.ReadAuthorizedControlSpec(client)
		if readErr != nil {
			s.log(ctx,
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
			s.log(ctx,
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
		s.handleControl(ctx, client, required, header.Token, spec, hash, required.namespace)
	case tunnel.CommandTraffic:
		request, readErr := tunnel.ReadTrafficOpenBody(client)
		if readErr != nil {
			s.log(
				ctx, required.requestID, "Gateway traffic stream rejected",
				"reason", "invalid_request", "error", readErr,
			)
			_ = tunnel.WriteStatus(client, errors.New("traffic request is invalid"))
			_ = client.Close()
			return
		}
		authorizationErr := s.authorizedNetwork(header.Token, &required)
		if authorizationErr != nil {
			s.log(ctx,
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
			s.log(ctx, required.requestID, "Gateway traffic stream rejected", "reason", "invalid_identity")
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
		startedAt := time.Now()
		if s.Logger != nil {
			s.Logger.InfoContext(
				ctx, "Gateway traffic relay started",
				"operation", "gateway.traffic.relay",
				"outcome", "started",
				"correlation_id", utils.CorrelationID(ctx),
				"request_id", required.requestID,
				"session_id", identity.SessionID,
				"session_generation", identity.SessionGeneration,
				"ticket_id", required.ticketID,
				"task_id", request.TaskID,
				"mode", request.Mode,
			)
		}
		traffic.ServeTraffic(ctx, client, identity, request)
		if s.Logger != nil {
			s.Logger.InfoContext(
				ctx, "Gateway traffic relay completed",
				"operation", "gateway.traffic.relay",
				"outcome", "completed",
				"correlation_id", utils.CorrelationID(ctx),
				"request_id", required.requestID,
				"session_id", identity.SessionID,
				"session_generation", identity.SessionGeneration,
				"ticket_id", required.ticketID,
				"task_id", request.TaskID,
				"mode", request.Mode,
				"duration_ms", time.Since(startedAt).Milliseconds(),
			)
		}
	default:
		s.log(ctx, required.requestID, "Gateway tunnel command rejected", "command", header.Command)
		_ = tunnel.WriteStatus(client, fmt.Errorf("unsupported command %d", header.Command))
		_ = client.Close()
	}
}

func (s *Server) authorizedNetwork(
	token tunnel.SessionToken,
	required *requiredAuthorization,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tenants[token] <= 0 {
		return errors.New("gateway session is not active")
	}
	if required == nil {
		return errors.New("gateway NetworkSpec authorization is not active")
	}
	network, ok := s.networks[token]
	if !ok || network.hash != required.networkSpecHash || network.namespace != required.namespace {
		return errors.New("gateway NetworkSpec authorization is not active")
	}
	return nil
}

func validNetworkSpecHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func (s *Server) log(ctx context.Context, requestID, message string, attributes ...any) {
	if s.Logger != nil {
		arguments := make([]any, 0, len(attributes)+6)
		arguments = append(
			arguments,
			"operation", "gateway.tunnel.stream", "outcome", "failure",
			"correlation_id", utils.CorrelationID(ctx), "request_id", requestID,
		)
		arguments = append(arguments, attributes...)
		s.Logger.WarnContext(ctx, message, arguments...)
	}
}
