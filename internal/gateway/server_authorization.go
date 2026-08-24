package gateway

import (
	"context"
	"net"
	"slices"

	"github.com/fengqi-dev/kube-loop/internal/dnsname"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficcontrol"
	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
)

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
	if !dnsname.ValidLabel(authorization.Namespace) {
		s.log(ctx, authorization.RequestID, "Gateway logical connection rejected", "reason", "invalid_namespace")
		_ = connection.Close()
		return
	}
	required := requiredAuthorization{
		requestID: authorization.RequestID, token: token,
		ticketID:  authorization.TicketID,
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
