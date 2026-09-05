package gateway

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/gateway/relay/agent"
	"github.com/fengqi-dev/kube-loop/internal/gateway/websocketmux"
	"github.com/fengqi-dev/kube-loop/internal/protocol/dns"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relaycontrol"
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
	TicketID        string
	Namespace       string
	NetworkSpecHash string
}

type tenantNetwork struct {
	hash      string
	namespace string
}

type Server struct {
	Logger  *slog.Logger
	Forward ForwardSessionRuntime

	mu            sync.Mutex
	tenants       map[tunnel.SessionToken]int
	networks      map[tunnel.SessionToken]tenantNetwork
	connections   map[net.Conn]struct{}
	traffic       TrafficHandler
	draining      bool
	connectionsWG sync.WaitGroup
}

// ForwardSessionRuntime owns the per-Session forward data path. The existing
// control connection remains authoritative for its lifetime.
type ForwardSessionRuntime interface {
	Register(context.Context, string, uint64, string, string, networkspec.Spec) error
	Release(string, uint64)
}

type TrafficHandler interface {
	ServeTraffic(context.Context, net.Conn, trafficcontrol.Identity, tunnel.TrafficOpenRequest)
}

func NewServer(logger *slog.Logger) *Server {
	return &Server{
		Logger:      logger,
		tenants:     make(map[tunnel.SessionToken]int),
		networks:    make(map[tunnel.SessionToken]tenantNetwork),
		connections: make(map[net.Conn]struct{}),
	}
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
		s.log(ctx, authorization.RequestID, "Gateway logical connection rejected", "reason", "invalid_session")
		_ = connection.Close()
		return
	}
	if !validNetworkSpecHash(authorization.NetworkSpecHash) {
		s.log(ctx, authorization.RequestID, "Gateway logical connection rejected", "reason", "invalid_network_spec")
		_ = connection.Close()
		return
	}
	if !dns.ValidLabel(authorization.Namespace) {
		s.log(ctx, authorization.RequestID, "Gateway logical connection rejected", "reason", "invalid_namespace")
		_ = connection.Close()
		return
	}
	required := requiredAuthorization{
		requestID: authorization.RequestID, token: token,
		ticketID: authorization.TicketID, namespace: authorization.Namespace,
		networkSpecHash: authorization.NetworkSpecHash,
		identity: trafficcontrol.Identity{
			IdentityID: authorization.IdentityID, Groups: slices.Clone(authorization.Groups),
			DeviceID: authorization.DeviceID, SessionID: authorization.SessionID,
			SessionGeneration: authorization.Generation, Namespace: authorization.Namespace,
		},
	}
	s.serveConn(ctx, connection, required)
}

type requiredAuthorization struct {
	requestID       string
	ticketID        string
	token           tunnel.SessionToken
	namespace       string
	networkSpecHash string
	identity        trafficcontrol.Identity
}

func (s *Server) serveConn(ctx context.Context, connection net.Conn, required requiredAuthorization) {
	if !s.trackConnection(connection) {
		s.log(ctx, required.requestID, "Gateway logical connection rejected", "reason", "draining")
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

type Reporter struct {
	Gateway         *Server
	WebSocket       *websocketmux.Handler
	Forward         sessionAdmissions
	MaximumPhysical uint32
	MaximumLogical  uint32
}

type sessionAdmissions interface {
	BeginDrain()
	Draining() bool
	ActiveSessions() int
}

func (reporter *Reporter) Snapshot() (relaycontrol.State, relaycontrol.Capacity) {
	state := relaycontrol.StateReady
	if reporter.Gateway.Draining() || reporter.WebSocket.Draining() ||
		(reporter.Forward != nil && reporter.Forward.Draining()) {
		state = relaycontrol.StateDraining
	}
	return state, relaycontrol.Capacity{
		MaximumPhysicalConnections: reporter.MaximumPhysical,
		MaximumLogicalStreams:      reporter.MaximumLogical,
		//nolint:gosec // The WebSocket limiter keeps active sessions within the validated uint32 maximum.
		ActivePhysicalConnections: uint32(reporter.WebSocket.ActiveSessions() + activeSessions(reporter.Forward)),
		//nolint:gosec // The Gateway tracks logical connections within the validated uint32 maximum.
		ActiveLogicalStreams: uint32(reporter.Gateway.ActiveConnections()),
	}
}

func activeSessions(admissions sessionAdmissions) int {
	if admissions == nil {
		return 0
	}
	return admissions.ActiveSessions()
}

func (reporter *Reporter) BeginDrain() {
	reporter.Gateway.BeginDrain()
	reporter.WebSocket.BeginDrain()
	if reporter.Forward != nil {
		reporter.Forward.BeginDrain()
	}
}

type Gateway interface {
	Draining() bool
	ActiveConnections() int
}

type RelayReadiness interface{ Ready() bool }

type OperationsState struct {
	Gateway Gateway
	Agent   RelayReadiness
}

func (state OperationsState) Ready() bool {
	if state.Gateway == nil || state.Gateway.Draining() {
		return false
	}
	return state.Agent == nil || state.Agent.Ready()
}

func (state OperationsState) Draining() bool {
	return state.Gateway != nil && state.Gateway.Draining()
}

func (state OperationsState) ActiveConnections() int {
	if state.Gateway == nil {
		return 0
	}
	return state.Gateway.ActiveConnections()
}

var _ Gateway = (*Server)(nil)
var _ RelayReadiness = (*agent.Agent)(nil)

// handleControl keeps the immutable NetworkSpec authorization active for the
// lifetime of a Data Plane Session.
func (s *Server) handleControl(
	ctx context.Context,
	client net.Conn,
	authorization requiredAuthorization,
	token tunnel.SessionToken,
	spec networkspec.Spec,
	networkSpecHash string,
	namespace string,
) {
	defer func() { _ = client.Close() }()
	if s.Forward != nil {
		if err := s.Forward.Register(
			ctx, authorization.identity.SessionID, authorization.identity.SessionGeneration,
			namespace, networkSpecHash, spec,
		); err != nil {
			_ = tunnel.WriteStatus(client, fmt.Errorf("start Session forward runtime: %w", err))
			return
		}
		defer s.Forward.Release(
			authorization.identity.SessionID, authorization.identity.SessionGeneration,
		)
	}
	s.mu.Lock()
	if existing, ok := s.networks[token]; ok &&
		(existing.hash != networkSpecHash || existing.namespace != namespace) {
		s.mu.Unlock()
		_ = tunnel.WriteStatus(client, errors.New("session NetworkSpec changed"))
		return
	}
	s.networks[token] = tenantNetwork{hash: networkSpecHash, namespace: namespace}
	s.tenants[token]++
	s.mu.Unlock()
	defer s.removeControl(token)
	if err := tunnel.WriteStatus(client, nil); err != nil {
		return
	}
	var unexpected [1]byte
	_, _ = client.Read(unexpected[:])
}

func (s *Server) removeControl(token tunnel.SessionToken) {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := s.tenants[token]
	if count <= 1 {
		delete(s.tenants, token)
		delete(s.networks, token)
		return
	}
	s.tenants[token] = count - 1
}

type gatewayRuntimeOptions struct {
	Context          context.Context
	Logger           *log.Logger
	ListenAddress    string
	Path             string
	Listener         net.Listener
	Handler          http.Handler
	Gateway          gatewayDrainRuntime
	Admissions       gatewayAdmissionRuntime
	Control          gatewayControlRuntime
	DrainTimeout     time.Duration
	ServeStopTimeout time.Duration
	Serve            gatewayServeFunc
}

type gatewayDrainRuntime interface {
	BeginDrain()
	Drain(context.Context) error
}

type gatewayAdmissionRuntime interface{ BeginDrain() }

type admissionGroup []gatewayAdmissionRuntime

func (group admissionGroup) BeginDrain() {
	for _, admissions := range group {
		if admissions != nil {
			admissions.BeginDrain()
		}
	}
}

type gatewayControlRuntime interface{ Drain(context.Context) error }
type gatewayAgentLifecycle interface {
	Stop()
	Done() <-chan struct{}
}
type gatewayServeFunc func(context.Context, net.Listener, http.Handler) error

var (
	_ gatewayDrainRuntime     = (*Server)(nil)
	_ gatewayAdmissionRuntime = (*websocketmux.Handler)(nil)
	_ gatewayControlRuntime   = (*agent.Agent)(nil)
	_ gatewayAgentLifecycle   = (*agent.Agent)(nil)
)

func stopGatewayAgent(ctx context.Context, agent gatewayAgentLifecycle) error {
	agent.Stop()
	select {
	case <-agent.Done():
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait for Relay agent shutdown: %w", ctx.Err())
	}
}

func serveGateway(options gatewayRuntimeOptions) error {
	errCh := make(chan error, 1)
	httpContext, cancelHTTP := context.WithCancel(context.WithoutCancel(options.Context))
	go func() {
		options.Logger.Printf("WebSocket Gateway listening on %s%s", options.ListenAddress, options.Path)
		errCh <- options.Serve(httpContext, options.Listener, options.Handler)
	}()

	serveFinished := false
	var serveError error
	select {
	case err := <-errCh:
		serveFinished = true
		serveError = err
	case <-options.Context.Done():
	}
	options.Logger.Printf("Gateway draining for up to %s", options.DrainTimeout)
	options.Gateway.BeginDrain()
	options.Admissions.BeginDrain()
	drainReportContext, cancelDrainReport := context.WithTimeout(
		context.WithoutCancel(options.Context), 5*time.Second,
	)
	if err := options.Control.Drain(drainReportContext); err != nil {
		options.Logger.Printf("report Data Plane drain failed: %v", err)
	}
	cancelDrainReport()
	drainContext, cancelDrain := context.WithTimeout(
		context.WithoutCancel(options.Context), options.DrainTimeout,
	)
	drainErr := options.Gateway.Drain(drainContext)
	cancelDrain()
	if drainErr != nil {
		options.Logger.Printf("Gateway drain deadline reached: %v", drainErr)
	}
	cancelHTTP()
	if !serveFinished {
		serveStopContext, cancelServeStop := context.WithTimeout(
			context.WithoutCancel(options.Context), options.ServeStopTimeout,
		)
		select {
		case err := <-errCh:
			serveError = err
		case <-serveStopContext.Done():
			serveError = fmt.Errorf("wait for Gateway listener shutdown: %w", serveStopContext.Err())
		}
		cancelServeStop()
	}
	if serveError != nil {
		return fmt.Errorf("gateway listener stopped: %w", serveError)
	}
	options.Logger.Print("Gateway stopped")
	return nil
}
