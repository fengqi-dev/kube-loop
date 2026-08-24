package gateway

import (
	"context"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
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
