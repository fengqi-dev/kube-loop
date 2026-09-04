package gateway

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/middleware"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
	"github.com/fengqi-dev/kube-loop/internal/transport/streamcopy"
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
	case tunnel.CommandTCP, tunnel.CommandUDP:
		s.handleOutbound(ctx, client, header, &required)
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
		s.handleControl(client, header.Token, spec, hash, required.namespace)
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
		_, authorized, authorizationErr := s.authorizedNetwork(header.Token, &required)
		if authorizationErr != nil || !authorized {
			if authorizationErr == nil {
				authorizationErr = errors.New("gateway NetworkSpec authorization is not active")
			}
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
				"correlation_id", middleware.ID(ctx),
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
				"correlation_id", middleware.ID(ctx),
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
		s.log(
			ctx, required.requestID, "Gateway tunnel open rejected",
			"reason", "authorization", "error", authorizationErr,
		)
		return
	}
	request, err := tunnel.ReadOpenBody(client, header.Command)
	if err != nil {
		s.log(ctx, required.requestID, "Gateway tunnel open rejected", "remote", client.RemoteAddr(), "error", err)
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
		s.log(ctx, required.requestID, "Gateway target denied", "target", request.Address(), "error", err)
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
			ctx, required.requestID, "Gateway target connection failed",
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
	streamcopy.Bidirectional(client, target)
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

func (s *Server) relayUDP(client, target net.Conn) {
	var once sync.Once
	stop := func() { once.Do(func() { _ = target.Close(); _ = client.Close() }) }
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		defer stop()
		reader := bufio.NewReader(client)
		var buffer []byte
		for {
			payload, err := tunnel.ReadDatagram(reader, buffer)
			if err != nil {
				return
			}
			buffer = payload[:0]
			if _, err := target.Write(payload); err != nil {
				return
			}
		}
	}()
	buffer := make([]byte, tunnel.MaxDatagramSize)
	for {
		read, err := target.Read(buffer)
		if err != nil {
			stop()
			<-readerDone
			return
		}
		if err := tunnel.WriteDatagram(client, buffer[:read]); err != nil {
			stop()
			<-readerDone
			return
		}
	}
}

func resolvePrivate(ctx context.Context, host string, port uint16) (string, error) {
	if strings.EqualFold(host, "localhost") {
		return "", errors.New("loopback targets are not allowed")
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return "", fmt.Errorf("resolve target: %w", err)
	}
	for _, address := range addresses {
		ip, ok := netip.AddrFromSlice(address.AsSlice())
		if !ok {
			continue
		}
		ip = ip.Unmap()
		if isClusterAddress(ip) {
			return net.JoinHostPort(ip.String(), strconv.FormatUint(uint64(port), 10)), nil
		}
	}
	return "", fmt.Errorf("target %q does not resolve to a private cluster address", host)
}

func isClusterAddress(ip netip.Addr) bool {
	return ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsMulticast()
}

func (s *Server) log(ctx context.Context, requestID, message string, attributes ...any) {
	if s.Logger != nil {
		arguments := make([]any, 0, len(attributes)+6)
		arguments = append(
			arguments,
			"operation", "gateway.tunnel.stream", "outcome", "failure",
			"correlation_id", middleware.ID(ctx), "request_id", requestID,
		)
		arguments = append(arguments, attributes...)
		s.Logger.WarnContext(ctx, message, arguments...)
	}
}
