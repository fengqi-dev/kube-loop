package dataplane

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/client/socksbridge"
	"github.com/fengqi-dev/kube-loop/internal/client/websocketmux"
	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
	"github.com/fengqi-dev/kube-loop/internal/singbox"
	"github.com/fengqi-dev/kube-loop/internal/trafficinspect"
)

const (
	DefaultListenAddress    = "127.0.0.1:1080"
	DefaultStartTimeout     = 15 * time.Second
	DefaultRecoveryAttempts = 5
	DefaultRecoveryBackoff  = 500 * time.Millisecond
	maxRuntimeLogLines      = 5_000
)

type Config struct {
	ListenAddress     string
	StartTimeout      time.Duration
	ClientVersion     string
	TLSConfig         *tls.Config
	TUNStarter        TUNStarter
	RecoveryAttempts  int
	RecoveryBackoff   time.Duration
	OnStatus          func(StatusEvent)
	TrafficInspection TrafficInspectionConfig

	startForwarder func(context.Context, websocketmux.ClientConfig) (streamForwarder, error)
	listenSOCKS    func(context.Context, string, string, tunnel.SessionToken) (localBridge, error)
	dialContext    func(context.Context, string, string) (net.Conn, error)
}

// TrafficInspectionConfig controls the optional in-process HTTP and gRPC
// inspection path. The zero value is disabled and preserves the existing
// SOCKS-to-Relay forwarding behavior.
type TrafficInspectionConfig struct {
	Enabled       bool
	IsEnabled     func() bool
	AuthorityPath string
	TLSConfig     *tls.Config
	OnRequest     func(*http.Request)
	OnResponse    func(*http.Response)
	Sink          trafficinspect.Sink
	Policy        trafficinspect.CapturePolicy
	Protobuf      *trafficinspect.ProtobufDecoder
	OnSinkError   func(error)
}

type streamForwarder interface {
	Address() string
	OpenStream(context.Context) (net.Conn, error)
	Close() error
}

type localBridge interface {
	Addr() net.Addr
	SetGatewayAddress(string)
	SetGateway(string, tunnel.SessionToken)
	SetHostTCPHandler(socksbridge.HostTCPHandler)
	SetLogHandler(socksbridge.LogHandler)
	Close() error
}

type openedTransport struct {
	forwarder streamForwarder
	control   net.Conn
	token     tunnel.SessionToken
}

type transportStreams struct {
	forwarder streamForwarder
	control   net.Conn
	count     int
	draining  bool
}

type trackedTrafficConn struct {
	net.Conn

	once    sync.Once
	release func()
}

func (connection *trackedTrafficConn) Close() error {
	err := connection.Conn.Close()
	connection.once.Do(connection.release)
	return err
}

func (runtime *Runtime) SetHostTCPHandler(handler socksbridge.HostTCPHandler) {
	runtime.bridge.SetHostTCPHandler(handler)
}

type Runtime struct {
	ctx          context.Context
	cancel       context.CancelFunc
	forwarder    streamForwarder
	control      net.Conn
	token        tunnel.SessionToken
	bridge       localBridge
	status       Status
	session      remote.Session
	tun          singbox.RunningCore
	tunCancel    context.CancelFunc
	tunWG        sync.WaitGroup
	tunStarter   TUNStarter
	dnsNamespace string
	hostAliases  []singbox.HostAlias
	config       Config

	closeOnce     sync.Once
	closeErr      error
	done          chan struct{}
	stateMu       sync.Mutex
	transportMu   sync.Mutex
	transportDone chan struct{}
	transportErr  error
	streams       map[chan struct{}]*transportStreams
	transportWG   sync.WaitGroup
	errMu         sync.Mutex
	err           error
	logMu         sync.Mutex
	socksLogs     []string
}

type Status struct {
	State             string `json:"state"`
	Mode              string `json:"mode"`
	SessionID         string `json:"sessionId"`
	SessionGeneration uint64 `json:"sessionGeneration"`
	SOCKSAddress      string `json:"socksAddress"`
	NetworkSpecHash   string `json:"networkSpecHash"`
}

type StatusEvent struct {
	ProfileID string `json:"profileId"`
	Status    Status `json:"status"`
	Error     string `json:"error,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Retryable bool   `json:"retryable,omitempty"`
}

type TUNStarter interface {
	Start(context.Context, singbox.NetworkSpec, string, string, []singbox.HostAlias) (singbox.RunningCore, error)
}
