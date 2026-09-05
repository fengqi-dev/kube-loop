package dataplane

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/client/socksbridge"
	"github.com/fengqi-dev/kube-loop/internal/client/websocketmux"
	"github.com/fengqi-dev/kube-loop/internal/protocol/sessionspec"
	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
	"github.com/fengqi-dev/kube-loop/internal/singbox"
)

const (
	DefaultListenAddress    = "127.0.0.1:1080"
	DefaultStartTimeout     = 15 * time.Second
	DefaultRecoveryAttempts = 5
	DefaultRecoveryBackoff  = 500 * time.Millisecond
	maxRuntimeLogLines      = 5_000
)

type Config struct {
	ListenAddress string
	StartTimeout  time.Duration
	ClientVersion string
	TLSConfig     *tls.Config
	// TrafficEncryption controls the client-to-Gateway traffic stream.
	// nil means enabled; a false value is an explicit compatibility opt-out.
	TrafficEncryption *bool
	TUNStarter        TUNStarter
	RecoveryAttempts  int
	RecoveryBackoff   time.Duration
	OnStatus          func(StatusEvent)
	Logger            *slog.Logger
	ForwardStart      ForwardStartFunc
	startForwarder    func(context.Context, websocketmux.ClientConfig) (streamForwarder, error)
	listenSOCKS       func(context.Context, string) (localBridge, error)
	dialContext       func(context.Context, string, string) (net.Conn, error)
}

type ForwardOptions struct {
	SessionID   string
	Generation  uint64
	Endpoint    string
	RelayTicket string
	TLSInsecure bool
	LogLevel    string
}

type ForwardCore interface {
	Address() string
	Done() <-chan struct{}
	Err() error
	Close() error
}

type ForwardStartFunc func(context.Context, ForwardOptions) (ForwardCore, error)

type streamForwarder interface {
	Address() string
	OpenStream(context.Context) (net.Conn, error)
	Close() error
}

type localBridge interface {
	Addr() net.Addr
	SetForwardDialer(socksbridge.ForwardDialer)
	SetHostTCPHandler(socksbridge.HostTCPHandler)
	SetLogHandler(socksbridge.LogHandler)
	Close() error
}

type openedTransport struct {
	forwarder         streamForwarder
	control           net.Conn
	token             tunnel.SessionToken
	trafficEncryption bool
	noisePublicKey    []byte
	forward           ForwardCore
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
	forward      ForwardCore
	dnsNamespace string
	hostAliases  []sessionspec.HostAlias
	config       Config

	closeOnce            sync.Once
	closeErr             error
	done                 chan struct{}
	stateMu              sync.Mutex
	transportMu          sync.Mutex
	transportDone        chan struct{}
	transportErr         error
	trafficEncryption    bool
	trafficEncryptionSet bool
	noisePublicKey       []byte
	streams              map[chan struct{}]*transportStreams
	transportWG          sync.WaitGroup
	errMu                sync.Mutex
	err                  error
	logMu                sync.Mutex
	socksLogs            []string
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
	Start(context.Context, singbox.NetworkSpec, string, string, []sessionspec.HostAlias) (singbox.RunningCore, error)
}
