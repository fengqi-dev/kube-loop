package gateway

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/dnsname"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/protocol/streamcopy"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficcontrol"
	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
)

type SessionAuthorization struct {
	RequestID       string
	IdentityID      string
	Groups          []string
	DeviceID        string
	SessionID       string
	Generation      uint64
	Namespace       string
	NetworkSpecHash string
}

type tenantNetwork struct {
	spec      networkspec.Spec
	hash      string
	namespace string
}

type Server struct {
	Logger      *slog.Logger
	DialTimeout time.Duration
	Resolver    IPResolver
	Dialer      ContextDialer

	mu            sync.Mutex
	tenants       map[tunnel.SessionToken]int
	networks      map[tunnel.SessionToken]tenantNetwork
	connections   map[net.Conn]struct{}
	traffic       TrafficHandler
	draining      bool
	connectionsWG sync.WaitGroup
}

type TrafficHandler interface {
	ServeTraffic(context.Context, net.Conn, trafficcontrol.Identity, tunnel.TrafficOpenRequest)
}

type ContextDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

func NewServer(logger *slog.Logger, dialTimeout time.Duration) *Server {
	if dialTimeout == 0 {
		dialTimeout = 10 * time.Second
	}
	return &Server{
		Logger:      logger,
		DialTimeout: dialTimeout,
		tenants:     make(map[tunnel.SessionToken]int),
		networks:    make(map[tunnel.SessionToken]tenantNetwork),
		connections: make(map[net.Conn]struct{}),
	}
}

// ServeConnForAuthorization handles a logical protocol connection carried by
// an authenticated WebSocket. The protocol key and registered NetworkSpec must
// match the immutable Cluster Session claims in its RelayTicket.
func (s *Server) ServeConnForAuthorization(connection net.Conn, authorization SessionAuthorization) {
	s.ServeConnForAuthorizationContext(context.Background(), connection, authorization)
}

// ServeConnForAuthorizationContext is ServeConnForAuthorization with the
// authenticated outer WebSocket request context propagated to the logical
// stream lifecycle.
func (s *Server) ServeConnForAuthorizationContext(
	ctx context.Context,
	connection net.Conn,
	authorization SessionAuthorization,
) {
	if ctx == nil {
		ctx = context.Background()
	}
	token, err := tunnel.RelaySessionToken(authorization.SessionID, authorization.Generation)
	if err != nil {
		s.log(authorization.RequestID, "Gateway logical connection rejected", "reason", "invalid_session")
		_ = connection.Close()
		return
	}
	if !validNetworkSpecHash(authorization.NetworkSpecHash) {
		s.log(authorization.RequestID, "Gateway logical connection rejected", "reason", "invalid_network_spec")
		_ = connection.Close()
		return
	}
	if !dnsname.ValidLabel(authorization.Namespace) {
		s.log(authorization.RequestID, "Gateway logical connection rejected", "reason", "invalid_namespace")
		_ = connection.Close()
		return
	}
	required := requiredAuthorization{
		requestID: authorization.RequestID, token: token,
		namespace: authorization.Namespace, networkSpecHash: authorization.NetworkSpecHash,
		identity: trafficcontrol.Identity{
			IdentityID:        authorization.IdentityID,
			Groups:            slices.Clone(authorization.Groups),
			DeviceID:          authorization.DeviceID,
			SessionID:         authorization.SessionID,
			SessionGeneration: authorization.Generation,
			Namespace:         authorization.Namespace,
		},
	}
	s.serveConn(ctx, connection, required)
}

type requiredAuthorization struct {
	requestID       string
	token           tunnel.SessionToken
	namespace       string
	networkSpecHash string
	identity        trafficcontrol.Identity
}

func (s *Server) serveConn(ctx context.Context, connection net.Conn, required requiredAuthorization) {
	if !s.trackConnection(connection) {
		s.log(required.requestID, "Gateway logical connection rejected", "reason", "draining")
		_ = connection.Close()
		return
	}
	defer s.untrackConnection(connection)
	s.handle(ctx, connection, required)
}

func (s *Server) SetTrafficHandler(handler TrafficHandler) {
	s.mu.Lock()
	s.traffic = handler
	s.mu.Unlock()
}

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
		<-done
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
	done := make(chan struct{})
	var once sync.Once
	stop := func() {
		once.Do(func() {
			close(done)
			_ = target.Close()
			_ = client.Close()
		})
	}
	go func() {
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
			<-done
			return
		}
		if err := tunnel.WriteDatagram(client, buffer[:read]); err != nil {
			stop()
			<-done
			return
		}
	}
}

func relayTCP(left, right net.Conn) {
	streamcopy.Bidirectional(left, right)
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

func (s *Server) log(requestID, message string, attributes ...any) {
	if s.Logger != nil {
		arguments := make([]any, 0, len(attributes)+2)
		arguments = append(arguments, "request_id", requestID)
		arguments = append(arguments, attributes...)
		s.Logger.WarnContext(context.Background(), message, arguments...)
	}
}
