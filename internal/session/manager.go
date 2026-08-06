package session

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/cluster"
	"github.com/fengqi-dev/kube-loop/internal/intercept"
	"github.com/fengqi-dev/kube-loop/internal/intercept/clusteradapter"
	"github.com/fengqi-dev/kube-loop/internal/podssh"
	"github.com/fengqi-dev/kube-loop/internal/portfwd"
	portfwdclusteradapter "github.com/fengqi-dev/kube-loop/internal/portfwd/clusteradapter"
	"github.com/fengqi-dev/kube-loop/internal/singbox"
	singboxdist "github.com/fengqi-dev/kube-loop/internal/singbox/distribution"
	"github.com/fengqi-dev/kube-loop/internal/socksbridge"
	"github.com/fengqi-dev/kube-loop/internal/store"
	"github.com/fengqi-dev/kube-loop/internal/traffic"
)

type Phase string

const (
	PhaseIdle        Phase = "idle"
	PhaseChecking    Phase = "checking"
	PhaseInstalling  Phase = "installing-gateway"
	PhaseDiscovering Phase = "discovering-network"
	PhaseStarting    Phase = "starting-tunnel"
	PhaseConnected   Phase = "connected"
	PhaseError       Phase = "error"
)

const DefaultGatewayImage = "ghcr.io/fengqi-dev/kube-loop/gateway:latest"

// DefaultSOCKSPort is the preferred local SOCKS5 port. A session falls back
// to an OS-assigned port when this address is already occupied.
const DefaultSOCKSPort = 7890

// ResolveGatewayImage picks the Gateway image for this desktop build.
// KUBELOOP_GATEWAY_IMAGE wins; release builds pin the matching image tag.
func ResolveGatewayImage(appVersion string) string {
	if image := strings.TrimSpace(os.Getenv("KUBELOOP_GATEWAY_IMAGE")); image != "" {
		return image
	}
	if appVersion != "" && appVersion != "dev" {
		return "ghcr.io/fengqi-dev/kube-loop/gateway:" + appVersion
	}
	return DefaultGatewayImage
}

type Request struct {
	Context   string
	Namespace string
	Mode      ConnectionMode
}

type ConnectionMode string

const (
	ConnectionModeTUN   ConnectionMode = "tun"
	ConnectionModeSOCKS ConnectionMode = "socks"
)

type State struct {
	Phase           Phase                 `json:"phase"`
	Mode            ConnectionMode        `json:"mode,omitempty"`
	Context         string                `json:"context"`
	Namespace       string                `json:"namespace"`
	DNSNamespace    string                `json:"dnsNamespace,omitempty"`
	Message         string                `json:"message"`
	Error           string                `json:"error,omitempty"`
	DNSWarning      string                `json:"dnsWarning,omitempty"`
	Network         *NetworkDiagnostics   `json:"network,omitempty"`
	Discovery       *cluster.Discovery    `json:"discovery,omitempty"`
	Capabilities    *cluster.Capabilities `json:"capabilities,omitempty"`
	ScopeNamespaces []string              `json:"scopeNamespaces,omitempty"`
	GatewayManifest string                `json:"gatewayManifest,omitempty"`
	Pods            []cluster.PodInfo     `json:"pods,omitempty"`
	Services        []cluster.ServiceInfo `json:"services,omitempty"`
	Events          []LogEvent            `json:"events,omitempty"`
	CoreVersion     string                `json:"coreVersion,omitempty"`
	SOCKSPort       int                   `json:"socksPort,omitempty"`
	ConnectedAt     *time.Time            `json:"connectedAt,omitempty"`
	Metrics         *singbox.Metrics      `json:"metrics,omitempty"`
	// InventoryRevision increments only on Informer-driven inventory snapshots
	// (pod/service/deployment add/update/delete). UI lists should key off this
	// instead of UpdatedAt, which also advances on the metrics ticker.
	InventoryRevision int64     `json:"inventoryRevision"`
	KubernetesVersion string    `json:"kubernetesVersion,omitempty"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

// ClusterCatalog exposes read-only cluster inventory used by the desktop
// facade. Keeping it separate from connection lifecycle operations lets
// callers test and replace those concerns independently.
type ClusterCatalog interface {
	Contexts() ([]cluster.ContextInfo, error)
	Namespaces(context.Context, string) ([]string, error)
	ListServices(context.Context, string, string) ([]cluster.ServiceInfo, error)
	ListPods(context.Context, string, string) ([]cluster.PodInfo, error)
}

// ClusterConnection exposes the Kubernetes operations needed to establish and
// monitor a connected session.
type ClusterConnection interface {
	ServerVersion(context.Context, string) (string, error)
	Discover(context.Context, string, []string) (cluster.Discovery, error)
	WatchInventory(
		context.Context,
		string,
		[]string,
		func(cluster.InventorySnapshot),
	) (io.Closer, error)
	ProbeCapabilities(context.Context, string) (cluster.Capabilities, error)
}

// GatewayManager owns the in-cluster Gateway and its API-server channel.
type GatewayManager interface {
	GetGateway(context.Context, string) (cluster.GatewayInfo, error)
	EnsureGateway(context.Context, string, string) (cluster.GatewayInfo, error)
	StartPortForward(context.Context, string, string, uint16) (cluster.PortForward, error)
}

// ClusterProvider is the composition-root contract implemented by
// cluster.Provider. Manager stores each facet behind its narrow interface,
// while feature managers receive only their own consumer-defined contracts.
type ClusterProvider interface {
	ClusterCatalog
	ClusterConnection
	GatewayManager
	clusteradapter.Provider
	portfwdclusteradapter.Provider
}

type Core interface {
	Start(
		context.Context,
		singbox.NetworkSpec,
		string,
		string,
		[]singbox.HostAlias,
	) (singbox.RunningCore, error)
}

type BridgeFactory func(context.Context, string, string) (net.Listener, error)

type Option func(*Manager)

func WithCore(core Core) Option {
	return func(manager *Manager) { manager.core = core }
}

func WithBridgeFactory(factory BridgeFactory) Option {
	return func(manager *Manager) { manager.bridgeFactory = factory }
}

func WithGatewayImage(image string) Option {
	return func(manager *Manager) { manager.gatewayImage = image }
}

// WithPodSSHOptions customizes the embedded Pod SSH server. It is primarily
// useful for deterministic integration tests that provide an ephemeral key.
func WithPodSSHOptions(options ...podssh.Option) Option {
	return func(manager *Manager) {
		executor, ok := any(manager.connection).(podssh.Executor)
		if !ok {
			return
		}
		manager.podSSH = podssh.NewServer(executor, options...)
	}
}

type recentConnection struct {
	connection singbox.Connection
	lastSeen   time.Time
}

type connectionTraffic struct {
	upload   int64
	download int64
	at       time.Time
}

type Manager struct {
	catalog       ClusterCatalog
	connection    ClusterConnection
	gateway       GatewayManager
	core          Core
	bridgeFactory BridgeFactory
	gatewayImage  string
	store         *store.Store

	mu        sync.RWMutex
	stateHub  *stateHub
	cancel    context.CancelFunc
	done      chan struct{}
	intercept *intercept.Manager
	portfwd   *portfwd.Manager
	podSSH    *podssh.Server

	recentConnections map[string]recentConnection
	lastTraffic       map[string]connectionTraffic
	restoring         bool
	shuttingDown      bool
	runningCore       singbox.RunningCore
	trafficTracker    *traffic.Tracker
}

// Keep short-lived TUN connections visible between core snapshot polls.
const connectionRetainFor = 30 * time.Second

// Bound retained/published rows so Wails/React are not flooded by bursty TUN flows.
const (
	maxRetainedConnections  = 500
	maxPublishedConnections = 100
)

func NewManager(provider ClusterProvider, options ...Option) *Manager {
	manager := &Manager{
		catalog:    provider,
		connection: provider,
		gateway:    provider,
		bridgeFactory: func(
			ctx context.Context, gatewayAddress, listenAddress string,
		) (net.Listener, error) {
			return socksbridge.Listen(ctx, gatewayAddress, listenAddress)
		},
		gatewayImage: ResolveGatewayImage(""),
		intercept:    intercept.NewManager(clusteradapter.New(provider)),
		portfwd:      portfwd.NewManager(portfwdclusteradapter.New(provider)),
		stateHub: newStateHub(State{
			Phase: PhaseIdle, Message: "Disconnected", CoreVersion: singboxdist.Version, UpdatedAt: time.Now(),
		}),
	}
	if executor, ok := any(provider).(podssh.Executor); ok {
		manager.podSSH = podssh.NewServer(executor)
	}
	manager.core = newSingboxRuntime(manager.AppendLog)
	for _, option := range options {
		option(manager)
	}
	return manager
}

func (m *Manager) fail(ctx context.Context, state State, message string, err error) {
	if ctx.Err() != nil {
		return
	}
	state.Phase = PhaseError
	state.Message = message
	state.Error = err.Error()
	state.ConnectedAt = nil
	m.publish(state)
	m.AppendLog("ERROR", message+": "+err.Error())
}

func (m *Manager) publish(state State) {
	m.stateHub.publish(state)
}

func (m *Manager) publishMetrics(metrics *singbox.Metrics) {
	m.stateHub.publishMetrics(metrics)
}

func (state State) String() string {
	if state.Error != "" {
		return fmt.Sprintf("%s: %s", state.Message, state.Error)
	}
	return state.Message
}
