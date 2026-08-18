package dataplane

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/client/socksbridge"
	"github.com/fengqi-dev/kube-loop/internal/client/websocketmux"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficstream"
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
	tunStarter   TUNStarter
	dnsNamespace string
	hostAliases  []singbox.HostAlias
	config       Config

	closeOnce     sync.Once
	done          chan struct{}
	stateMu       sync.Mutex
	transportMu   sync.Mutex
	transportDone chan struct{}
	transportErr  error
	streams       map[chan struct{}]*transportStreams
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

func Start(
	ctx context.Context,
	serverProfile profile.Profile,
	session remote.Session,
	ticketSource func(context.Context) (remote.RelayTicket, error),
	config Config,
) (*Runtime, error) {
	if ctx == nil {
		return nil, errors.New("Data Plane context is required")
	}
	useDefaultListenAddress := strings.TrimSpace(config.ListenAddress) == ""
	config = normalizedConfig(config)
	runtimeCtx, cancel := context.WithCancel(ctx)
	transport, err := openTransport(runtimeCtx, serverProfile, session, ticketSource, config)
	if err != nil {
		cancel()
		return nil, err
	}
	bridge, err := config.listenSOCKS(runtimeCtx, transport.forwarder.Address(), config.ListenAddress, transport.token)
	if err != nil && useDefaultListenAddress && isAddressAlreadyInUse(err) {
		host, _, splitErr := net.SplitHostPort(config.ListenAddress)
		if splitErr == nil {
			bridge, err = config.listenSOCKS(
				runtimeCtx, transport.forwarder.Address(), net.JoinHostPort(host, "0"), transport.token,
			)
		}
	}
	if err != nil {
		_ = transport.control.Close()
		_ = transport.forwarder.Close()
		cancel()
		return nil, fmt.Errorf("start Data Plane SOCKS bridge: %w", err)
	}
	runtime := &Runtime{
		ctx: runtimeCtx, cancel: cancel, forwarder: transport.forwarder, control: transport.control,
		token:  transport.token,
		bridge: bridge, done: make(chan struct{}), transportDone: make(chan struct{}),
		session: session, tunStarter: config.TUNStarter, config: config,
		dnsNamespace: strings.TrimSpace(serverProfile.DNSNamespace),
		hostAliases:  profileHostAliases(serverProfile.HostAliases),
		status: Status{
			State: "connected", Mode: "socks", SessionID: session.ID, SessionGeneration: session.Generation,
			SOCKSAddress:    bridge.Addr().String(),
			NetworkSpecHash: session.NetworkSpecHash,
		},
	}
	bridge.SetLogHandler(runtime.appendSOCKSLog)
	runtime.appendSOCKSLog("listening on " + bridge.Addr().String())
	go runtime.watchControl(transport.control, runtime.transportDone)
	go runtime.watchContext(runtimeCtx)
	return runtime, nil
}

// OpenTrafficStream opens one reverse-traffic Task as a logical stream on the
// Runtime's current /tunnel WebSocket pool.
func (runtime *Runtime) OpenTrafficStream(
	ctx context.Context,
	mode string,
	taskID string,
) (*trafficstream.FrameConn, error) {
	if ctx == nil {
		return nil, errors.New("Traffic stream context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	runtime.transportMu.Lock()
	if runtime.ctx.Err() != nil || runtime.forwarder == nil || runtime.token == (tunnel.SessionToken{}) ||
		signalClosed(runtime.transportDone) {
		runtime.transportMu.Unlock()
		return nil, errors.New("Data Plane transport is not connected")
	}
	forwarder := runtime.forwarder
	control := runtime.control
	token := runtime.token
	transportDone := runtime.transportDone
	runtime.transportMu.Unlock()

	connection, err := forwarder.OpenStream(ctx)
	if err != nil {
		return nil, fmt.Errorf("open Data Plane logical stream: %w", err)
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = connection.Close()
		}
	}()
	clearDeadline, err := bindConnectionContext(ctx, connection)
	if err != nil {
		return nil, err
	}
	defer clearDeadline()
	if err := tunnel.WriteTrafficOpen(connection, tunnel.TrafficOpenRequest{Mode: mode, TaskID: taskID}, token); err != nil {
		return nil, fmt.Errorf("open Traffic Task stream: %w", contextConnectionError(ctx, err))
	}
	if err := tunnel.ReadStatus(connection); err != nil {
		return nil, fmt.Errorf("start Traffic Task stream: %w", contextConnectionError(ctx, err))
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	runtime.transportMu.Lock()
	current := runtime.ctx.Err() == nil && runtime.forwarder == forwarder && runtime.control == control && runtime.token == token &&
		runtime.transportDone == transportDone && !signalClosed(transportDone)
	if current {
		if runtime.streams == nil {
			runtime.streams = make(map[chan struct{}]*transportStreams)
		}
		streams := runtime.streams[transportDone]
		if streams == nil {
			streams = &transportStreams{forwarder: forwarder, control: control}
			runtime.streams[transportDone] = streams
		}
		streams.count++
		connection = &trackedTrafficConn{
			Conn: connection,
			release: func() {
				runtime.releaseTrafficStream(transportDone)
			},
		}
	}
	runtime.transportMu.Unlock()
	if !current {
		return nil, errors.New("Data Plane transport changed while opening Traffic Task stream")
	}
	framed, err := trafficstream.Dial(ctx, connection)
	if err != nil {
		return nil, fmt.Errorf("upgrade Traffic Task stream to WebSocket: %w", err)
	}
	closeOnError = false
	return framed, nil
}

func (runtime *Runtime) releaseTrafficStream(transportDone chan struct{}) {
	var draining *transportStreams
	runtime.transportMu.Lock()
	streams := runtime.streams[transportDone]
	if streams != nil {
		if streams.count > 0 {
			streams.count--
		}
		if streams.count == 0 {
			delete(runtime.streams, transportDone)
			if streams.draining {
				draining = streams
			}
		}
	}
	runtime.transportMu.Unlock()
	if draining != nil {
		_ = closeConnection(draining.control)
		_ = closeForwarder(draining.forwarder)
	}
}

func bindConnectionContext(ctx context.Context, connection net.Conn) (func(), error) {
	deadline := time.Time{}
	if value, ok := ctx.Deadline(); ok {
		deadline = value
	}
	if err := connection.SetDeadline(deadline); err != nil {
		return nil, fmt.Errorf("set Traffic stream startup deadline: %w", err)
	}
	finished := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = connection.SetDeadline(time.Now())
		close(finished)
	})
	return func() {
		if !stop() {
			<-finished
		}
		_ = connection.SetDeadline(time.Time{})
	}, nil
}

func contextConnectionError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}

func isAddressAlreadyInUse(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "address already in use") ||
		strings.Contains(message, "only one usage of each socket address")
}

func openTransport(
	ctx context.Context,
	serverProfile profile.Profile,
	session remote.Session,
	ticketSource func(context.Context) (remote.RelayTicket, error),
	config Config,
) (openedTransport, error) {
	if ticketSource == nil {
		return openedTransport{}, errors.New("RelayTicket source is required")
	}
	if strings.TrimSpace(serverProfile.ID) == "" || session.State != "active" {
		return openedTransport{}, errors.New("active Server Profile Session is required")
	}
	token, err := tunnel.RelaySessionToken(session.ID, session.Generation)
	if err != nil {
		return openedTransport{}, fmt.Errorf("derive Data Plane Session token: %w", err)
	}
	specHash, err := networkspec.Hash(session.NetworkSpec)
	if err != nil {
		return openedTransport{}, fmt.Errorf("validate Data Plane NetworkSpec: %w", err)
	}
	if specHash != session.NetworkSpecHash {
		return openedTransport{}, errors.New("Data Plane NetworkSpec hash does not match the Session")
	}
	ticket, err := ticketSource(ctx)
	if err != nil {
		return openedTransport{}, fmt.Errorf("obtain RelayTicket assignment: %w", err)
	}
	webSocketURL, err := transportURL(serverProfile, ticket.Endpoint)
	if err != nil {
		return openedTransport{}, err
	}
	boundSource := newAssignmentTokenSource(ticketSource, ticket)
	forwarder, err := config.startForwarder(ctx, websocketmux.ClientConfig{
		URL: webSocketURL, TokenSource: boundSource,
		TLSConfig: config.TLSConfig, ClientVersion: config.ClientVersion, DeviceID: ticket.DeviceID,
	})
	if err != nil {
		return openedTransport{}, fmt.Errorf("start Data Plane WebSocket transport: %w", err)
	}
	startCtx, startCancel := context.WithTimeout(ctx, config.StartTimeout)
	defer startCancel()
	control, err := config.dialContext(startCtx, "tcp", forwarder.Address())
	if err == nil {
		err = tunnel.WriteAuthorizedControlSession(control, token, session.NetworkSpec)
	}
	if err == nil {
		err = tunnel.ReadStatus(control)
	}
	if err != nil {
		if control != nil {
			_ = control.Close()
		}
		_ = forwarder.Close()
		return openedTransport{}, fmt.Errorf("register Data Plane Session authorization: %w", err)
	}
	return openedTransport{forwarder: forwarder, control: control, token: token}, nil
}

func transportURL(serverProfile profile.Profile, assignedEndpoint string) (string, error) {
	assignedEndpoint = strings.TrimSpace(assignedEndpoint)
	if assignedEndpoint == "" {
		return URL(serverProfile)
	}
	baseURL, err := profile.NormalizeBaseURL(serverProfile.BaseURL)
	if err != nil {
		return "", err
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", errors.New("Server Profile URL is invalid")
	}
	endpoint, err := url.Parse(assignedEndpoint)
	if err != nil || (endpoint.Scheme != "ws" && endpoint.Scheme != "wss") || endpoint.Host == "" {
		return "", errors.New("Relay assignment endpoint is invalid")
	}
	if base.Scheme == "http" {
		endpoint.Scheme = "ws"
	} else {
		endpoint.Scheme = "wss"
	}
	return endpoint.String(), nil
}

func newAssignmentTokenSource(
	source func(context.Context) (remote.RelayTicket, error),
	initial remote.RelayTicket,
) func(context.Context) (string, error) {
	var mutex sync.Mutex
	first := true
	return func(ctx context.Context) (string, error) {
		mutex.Lock()
		defer mutex.Unlock()
		ticket := initial
		if first {
			first = false
		} else {
			var err error
			ticket, err = source(ctx)
			if err != nil {
				return "", err
			}
			if ticket.Endpoint != initial.Endpoint || ticket.RelayID != initial.RelayID || ticket.DeviceID != initial.DeviceID {
				return "", errors.New("Relay assignment changed while opening a WebSocket pool")
			}
		}
		if ticket.Ticket == "" || strings.TrimSpace(ticket.Ticket) != ticket.Ticket {
			return "", errors.New("RelayTicket source returned an invalid ticket")
		}
		return ticket.Ticket, nil
	}
}

func URL(serverProfile profile.Profile) (string, error) {
	baseURL, err := profile.NormalizeBaseURL(serverProfile.BaseURL)
	if err != nil {
		return "", err
	}
	tunnelPath := strings.TrimSpace(serverProfile.TunnelPath)
	if tunnelPath == "" {
		tunnelPath = "/tunnel"
	}
	parsedPath, err := url.ParseRequestURI(tunnelPath)
	if err != nil || !strings.HasPrefix(tunnelPath, "/") || parsedPath.IsAbs() || parsedPath.Host != "" ||
		parsedPath.RawQuery != "" || parsedPath.Fragment != "" || parsedPath.EscapedPath() != tunnelPath ||
		strings.Contains(tunnelPath, "//") || strings.Contains(tunnelPath, "/./") || strings.Contains(tunnelPath, "/../") ||
		strings.HasSuffix(tunnelPath, "/.") || strings.HasSuffix(tunnelPath, "/..") {
		return "", errors.New("Server Profile tunnel path is invalid")
	}
	endpoint, err := url.Parse(baseURL)
	if err != nil {
		return "", errors.New("Server Profile URL is invalid")
	}
	switch endpoint.Scheme {
	case "https":
		endpoint.Scheme = "wss"
	case "http":
		endpoint.Scheme = "ws"
	default:
		return "", errors.New("Server Profile URL must use HTTP or HTTPS")
	}
	endpoint.Path = parsedPath.Path
	endpoint.RawPath = parsedPath.RawPath
	endpoint.RawQuery = ""
	endpoint.Fragment = ""
	return endpoint.String(), nil
}

func normalizedConfig(config Config) Config {
	config.ListenAddress = strings.TrimSpace(config.ListenAddress)
	if config.ListenAddress == "" {
		config.ListenAddress = DefaultListenAddress
	}
	if config.StartTimeout <= 0 {
		config.StartTimeout = DefaultStartTimeout
	}
	if config.TLSConfig != nil {
		config.TLSConfig = config.TLSConfig.Clone()
	}
	if config.TrafficInspection.TLSConfig != nil {
		config.TrafficInspection.TLSConfig = config.TrafficInspection.TLSConfig.Clone()
	}
	if config.startForwarder == nil {
		config.startForwarder = func(ctx context.Context, clientConfig websocketmux.ClientConfig) (streamForwarder, error) {
			return websocketmux.Start(ctx, clientConfig)
		}
	}
	if config.listenSOCKS == nil {
		inspection := config.TrafficInspection
		config.listenSOCKS = func(
			ctx context.Context,
			gatewayAddress, listenAddress string,
			token tunnel.SessionToken,
		) (localBridge, error) {
			if !inspection.Enabled && inspection.IsEnabled == nil {
				return socksbridge.Listen(ctx, gatewayAddress, listenAddress, token)
			}
			return socksbridge.Listen(
				ctx,
				gatewayAddress,
				listenAddress,
				token,
				socksbridge.WithTCPInspector(func(dialContext socksbridge.DialContextFunc) (socksbridge.TCPInspector, error) {
					authorityPath := strings.TrimSpace(inspection.AuthorityPath)
					if authorityPath == "" {
						var err error
						authorityPath, err = trafficinspect.DefaultAuthorityPath()
						if err != nil {
							return nil, err
						}
					}
					authority, err := trafficinspect.LoadOrCreateAuthority(authorityPath)
					if err != nil {
						return nil, err
					}
					return trafficinspect.New(trafficinspect.Config{
						CA:          authority.TLSCertificate(),
						DialContext: trafficinspect.DialContextFunc(dialContext),
						Enabled:     inspection.IsEnabled,
						OnRequest:   inspection.OnRequest,
						OnResponse:  inspection.OnResponse,
						Sink:        inspection.Sink,
						Policy:      inspection.Policy,
						OnSinkError: inspection.OnSinkError,
						TLSConfig:   inspection.TLSConfig,
						AllowHTTP2:  true,
					})
				}),
			)
		}
	}
	if config.dialContext == nil {
		config.dialContext = (&net.Dialer{}).DialContext
	}
	return config
}

func (runtime *Runtime) Status() Status {
	runtime.stateMu.Lock()
	defer runtime.stateMu.Unlock()
	return runtime.status
}

func (runtime *Runtime) StartTUN(ctx context.Context) (Status, error) {
	runtime.stateMu.Lock()
	defer runtime.stateMu.Unlock()
	return runtime.startTUNLocked(ctx)
}

func (runtime *Runtime) startTUNLocked(ctx context.Context) (Status, error) {
	if runtime.tun != nil {
		return runtime.status, nil
	}
	if runtime.tunStarter == nil {
		return Status{}, errors.New("TUN runtime is unavailable")
	}
	select {
	case <-runtime.done:
		return Status{}, errors.New("Data Plane runtime is closed")
	default:
	}
	spec := runtime.session.NetworkSpec
	// The caller context bounds startup only. Once ready, the TUN belongs to
	// the Data Plane Runtime and must survive the Wails RPC context ending.
	// The sing-box runtime watches its context for the entire process lifetime.
	tunCtx, tunCancel := context.WithCancel(runtime.ctx)
	stopStartupCancel := context.AfterFunc(ctx, tunCancel)
	namespace := runtime.dnsNamespace
	if namespace == "" {
		namespace = runtime.session.Namespace
	}
	core, err := runtime.tunStarter.Start(tunCtx, singbox.NetworkSpec{
		PodCIDRs: append([]string(nil), spec.PodCIDRs...), PodIPs: append([]string(nil), spec.PodIPs...),
		ServiceCIDRs: append([]string(nil), spec.ServiceCIDRs...),
		ServiceIPs:   append([]string(nil), spec.ServiceIPs...), DNSServer: spec.DNSServer,
		ClusterDomains: append([]string(nil), spec.ClusterDomains...),
	}, runtime.status.SOCKSAddress, namespace, append([]singbox.HostAlias{}, runtime.hostAliases...))
	stopStartupCancel()
	if err != nil {
		tunCancel()
		return Status{}, fmt.Errorf("start TUN: %w", err)
	}
	if err := ctx.Err(); err != nil {
		tunCancel()
		_ = core.Close()
		return Status{}, fmt.Errorf("start TUN: %w", err)
	}
	runtime.tun = core
	runtime.tunCancel = tunCancel
	runtime.status.Mode = "tun"
	go runtime.watchTUN(core)
	return runtime.status, nil
}

func (runtime *Runtime) StopTUN() (Status, error) {
	runtime.stateMu.Lock()
	core := runtime.tun
	cancel := runtime.tunCancel
	runtime.tun = nil
	runtime.tunCancel = nil
	runtime.status.Mode = "socks"
	status := runtime.status
	runtime.stateMu.Unlock()
	if core == nil {
		return status, nil
	}
	if cancel != nil {
		cancel()
	}
	if err := core.Close(); err != nil {
		return Status{}, err
	}
	return status, nil
}

func (runtime *Runtime) Metrics(ctx context.Context) (singbox.Metrics, error) {
	runtime.stateMu.Lock()
	core := runtime.tun
	runtime.stateMu.Unlock()
	if core == nil {
		return singbox.Metrics{}, errors.New("TUN runtime is not running")
	}
	return core.Snapshot(ctx)
}

func (runtime *Runtime) Logs(ctx context.Context) ([]string, error) {
	runtime.stateMu.Lock()
	core := runtime.tun
	runtime.stateMu.Unlock()
	runtime.logMu.Lock()
	logs := slices.Clone(runtime.socksLogs)
	runtime.logMu.Unlock()
	if core == nil {
		return logs, nil
	}
	tunLogs, err := core.ReadLogs(ctx)
	if err != nil {
		return nil, err
	}
	for _, line := range tunLogs {
		logs = append(logs, "[TUN] "+line)
	}
	return logs, nil
}

func (runtime *Runtime) appendSOCKSLog(message string) {
	line := time.Now().Format("15:04:05") + " [SOCKS] " + message
	runtime.logMu.Lock()
	runtime.socksLogs = append(runtime.socksLogs, line)
	if len(runtime.socksLogs) > maxRuntimeLogLines {
		runtime.socksLogs = slices.Clone(runtime.socksLogs[len(runtime.socksLogs)-maxRuntimeLogLines:])
	}
	runtime.logMu.Unlock()
}

func (runtime *Runtime) ConfigJSON() ([]byte, error) {
	runtime.stateMu.Lock()
	defer runtime.stateMu.Unlock()
	if runtime.tun == nil {
		return nil, errors.New("TUN runtime is not running")
	}
	return append([]byte{}, runtime.tun.Config()...), nil
}

func (runtime *Runtime) UpdateDNSNamespace(ctx context.Context, namespace string) error {
	namespace = strings.TrimSpace(namespace)
	runtime.stateMu.Lock()
	core := runtime.tun
	effectiveNamespace := namespace
	if effectiveNamespace == "" {
		effectiveNamespace = runtime.session.Namespace
	}
	if core == nil {
		runtime.dnsNamespace = namespace
		runtime.stateMu.Unlock()
		return nil
	}
	runtime.stateMu.Unlock()
	if err := core.UpdateDNSNamespace(ctx, effectiveNamespace); err != nil {
		return err
	}
	runtime.stateMu.Lock()
	runtime.dnsNamespace = namespace
	runtime.stateMu.Unlock()
	return nil
}

func (runtime *Runtime) UpdateHostAliases(ctx context.Context, aliases []singbox.HostAlias) error {
	normalized, err := singbox.NormalizeHostAliases(aliases)
	if err != nil {
		return err
	}
	runtime.stateMu.Lock()
	core := runtime.tun
	if core == nil {
		runtime.hostAliases = normalized
		runtime.stateMu.Unlock()
		return nil
	}
	runtime.stateMu.Unlock()
	if err := core.UpdateHostAliases(ctx, normalized); err != nil {
		return err
	}
	runtime.stateMu.Lock()
	runtime.hostAliases = normalized
	runtime.stateMu.Unlock()
	return nil
}

func profileHostAliases(items []profile.HostAlias) []singbox.HostAlias {
	aliases := make([]singbox.HostAlias, len(items))
	for index, item := range items {
		aliases[index] = singbox.HostAlias{Domain: item.Domain, IP: item.IP}
	}
	return aliases
}

func (runtime *Runtime) Done() <-chan struct{} { return runtime.done }

func (runtime *Runtime) TransportDone() <-chan struct{} {
	runtime.transportMu.Lock()
	defer runtime.transportMu.Unlock()
	return runtime.transportDone
}

func (runtime *Runtime) TransportErr() error {
	runtime.transportMu.Lock()
	defer runtime.transportMu.Unlock()
	return runtime.transportErr
}

func (runtime *Runtime) Err() error {
	runtime.errMu.Lock()
	defer runtime.errMu.Unlock()
	return runtime.err
}

func (runtime *Runtime) Close() error {
	var result error
	runtime.closeOnce.Do(func() {
		runtime.cancel()
		runtime.stateMu.Lock()
		core := runtime.tun
		cancelTUN := runtime.tunCancel
		runtime.tun = nil
		runtime.tunCancel = nil
		runtime.stateMu.Unlock()
		if cancelTUN != nil {
			cancelTUN()
		}
		if core != nil {
			result = errors.Join(result, core.Close())
		}
		runtime.transportMu.Lock()
		control := runtime.control
		forwarder := runtime.forwarder
		draining := make([]*transportStreams, 0, len(runtime.streams))
		for _, streams := range runtime.streams {
			if streams.draining {
				draining = append(draining, streams)
			}
		}
		runtime.control = nil
		runtime.forwarder = nil
		runtime.token = tunnel.SessionToken{}
		runtime.streams = nil
		closeSignal(runtime.transportDone)
		runtime.transportMu.Unlock()
		result = errors.Join(
			result,
			ignoreClosed(runtime.bridge.Close()),
			closeConnection(control),
			closeForwarder(forwarder),
		)
		for _, streams := range draining {
			result = errors.Join(result, closeConnection(streams.control), closeForwarder(streams.forwarder))
		}
		close(runtime.done)
	})
	return result
}

func (runtime *Runtime) Reconnect(
	ctx context.Context,
	serverProfile profile.Profile,
	session remote.Session,
	ticketSource func(context.Context) (remote.RelayTicket, error),
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// A successfully installed transport must live for the whole Runtime. A
	// short-lived reconnect context would cancel the WebSocket forwarder as
	// soon as this method returned and cause an immediate reconnect loop.
	transport, err := openTransport(runtime.ctx, serverProfile, session, ticketSource, runtime.config)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		_ = transport.control.Close()
		_ = transport.forwarder.Close()
		return err
	}
	runtime.stateMu.Lock()
	if session.State != "active" || session.ID != runtime.session.ID || session.Generation < runtime.session.Generation {
		runtime.stateMu.Unlock()
		_ = transport.control.Close()
		_ = transport.forwarder.Close()
		return errors.New("stale or changed Session generation during Data Plane recovery")
	}
	networkChanged := session.NetworkSpecHash != runtime.session.NetworkSpecHash
	generationChanged := session.Generation > runtime.session.Generation
	runtime.transportMu.Lock()
	transportStopped := signalClosed(runtime.transportDone)
	if runtime.ctx.Err() != nil || (!transportStopped && !networkChanged && !generationChanged) {
		runtime.transportMu.Unlock()
		runtime.stateMu.Unlock()
		_ = transport.control.Close()
		_ = transport.forwarder.Close()
		return errors.New("Data Plane runtime is not awaiting recovery")
	}
	restoreTUN := networkChanged && runtime.tun != nil
	if restoreTUN {
		core := runtime.tun
		cancelTUN := runtime.tunCancel
		runtime.tun = nil
		runtime.tunCancel = nil
		runtime.status.Mode = "socks"
		if cancelTUN != nil {
			cancelTUN()
		}
		if err := core.Close(); err != nil {
			runtime.transportMu.Unlock()
			runtime.stateMu.Unlock()
			_ = transport.control.Close()
			_ = transport.forwarder.Close()
			return fmt.Errorf("stop TUN for refreshed NetworkSpec: %w", err)
		}
	}
	var closeAfterSwap *transportStreams
	if !transportStopped {
		closeAfterSwap = runtime.drainTransportLocked(
			runtime.transportDone,
			runtime.control,
			runtime.forwarder,
		)
	}
	defer func() {
		if closeAfterSwap != nil {
			_ = closeConnection(closeAfterSwap.control)
			_ = closeForwarder(closeAfterSwap.forwarder)
		}
	}()
	runtime.forwarder = transport.forwarder
	runtime.control = transport.control
	runtime.token = transport.token
	transportDone := make(chan struct{})
	runtime.transportDone = transportDone
	runtime.transportErr = nil
	runtime.bridge.SetGateway(transport.forwarder.Address(), transport.token)
	runtime.session = session
	runtime.status.SessionID = session.ID
	runtime.status.SessionGeneration = session.Generation
	runtime.status.NetworkSpecHash = session.NetworkSpecHash
	runtime.status.State = "connected"
	if restoreTUN {
		if _, err := runtime.startTUNLocked(ctx); err != nil {
			runtime.transportMu.Unlock()
			runtime.stateMu.Unlock()
			_ = transport.control.Close()
			_ = transport.forwarder.Close()
			return fmt.Errorf("restore TUN for refreshed NetworkSpec: %w", err)
		}
	}
	runtime.transportMu.Unlock()
	runtime.stateMu.Unlock()
	go runtime.watchControl(transport.control, transportDone)
	return nil
}

// drainTransportLocked removes a transport from new traffic while preserving
// task-scoped streams already running on it. The final tracked stream closes
// the old control connection and forwarder. Callers must hold transportMu.
func (runtime *Runtime) drainTransportLocked(
	transportDone chan struct{},
	control net.Conn,
	forwarder streamForwarder,
) *transportStreams {
	closeSignal(transportDone)
	streams := runtime.streams[transportDone]
	if streams != nil && streams.count > 0 {
		streams.control = control
		streams.forwarder = forwarder
		streams.draining = true
		return nil
	}
	delete(runtime.streams, transportDone)
	if control == nil && forwarder == nil {
		return nil
	}
	return &transportStreams{control: control, forwarder: forwarder, draining: true}
}

func ignoreClosed(err error) error {
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

func closeConnection(connection net.Conn) error {
	if connection == nil {
		return nil
	}
	return ignoreClosed(connection.Close())
}

func closeForwarder(forwarder streamForwarder) error {
	if forwarder == nil {
		return nil
	}
	return ignoreClosed(forwarder.Close())
}

func closeSignal(signal chan struct{}) {
	if signal == nil || signalClosed(signal) {
		return
	}
	close(signal)
}

func signalClosed(signal <-chan struct{}) bool {
	if signal == nil {
		return true
	}
	select {
	case <-signal:
		return true
	default:
		return false
	}
}

func (runtime *Runtime) watchTUN(core singbox.RunningCore) {
	<-core.Done()
	runtime.stateMu.Lock()
	active := runtime.tun == core
	var cancel context.CancelFunc
	if active {
		runtime.tun = nil
		cancel = runtime.tunCancel
		runtime.tunCancel = nil
		runtime.status.Mode = "socks"
	}
	runtime.stateMu.Unlock()
	if !active {
		return
	}
	if cancel != nil {
		cancel()
	}
	err := core.Err()
	if err == nil {
		err = errors.New("TUN stopped unexpectedly")
	}
	runtime.errMu.Lock()
	runtime.err = err
	runtime.errMu.Unlock()
	_ = runtime.Close()
}

func (runtime *Runtime) watchControl(control net.Conn, transportDone chan struct{}) {
	var buffer [1]byte
	_, err := control.Read(buffer[:])
	if runtime.ctx.Err() != nil {
		return
	}
	if err == nil {
		err = errors.New("Data Plane control stream returned unexpected data")
	} else {
		err = fmt.Errorf("Data Plane control stream closed: %w", err)
	}
	runtime.transportMu.Lock()
	if runtime.control != control || runtime.transportDone != transportDone || runtime.ctx.Err() != nil {
		streams := runtime.streams[transportDone]
		if streams != nil && streams.draining && streams.control == control {
			delete(runtime.streams, transportDone)
		} else {
			streams = nil
		}
		runtime.transportMu.Unlock()
		if streams != nil {
			_ = closeConnection(streams.control)
			_ = closeForwarder(streams.forwarder)
		}
		return
	}
	forwarder := runtime.forwarder
	delete(runtime.streams, transportDone)
	runtime.control = nil
	runtime.forwarder = nil
	runtime.token = tunnel.SessionToken{}
	runtime.transportErr = err
	runtime.bridge.SetGatewayAddress("127.0.0.1:0")
	closeSignal(runtime.transportDone)
	runtime.transportMu.Unlock()
	_ = control.Close()
	_ = closeForwarder(forwarder)
}

// interruptTransport makes a live transport eligible for the same atomic
// replacement path used after an observed socket failure. It intentionally
// preserves the local SOCKS listener and TUN; callers must already have
// serialized recovery at the Manager layer.
func (runtime *Runtime) interruptTransport(reason error) {
	if reason == nil {
		reason = errors.New("Data Plane transport refresh requested")
	}
	runtime.transportMu.Lock()
	if runtime.ctx.Err() != nil || signalClosed(runtime.transportDone) {
		runtime.transportMu.Unlock()
		return
	}
	control := runtime.control
	forwarder := runtime.forwarder
	delete(runtime.streams, runtime.transportDone)
	runtime.control = nil
	runtime.forwarder = nil
	runtime.token = tunnel.SessionToken{}
	runtime.transportErr = reason
	runtime.bridge.SetGatewayAddress("127.0.0.1:0")
	closeSignal(runtime.transportDone)
	runtime.transportMu.Unlock()
	_ = closeConnection(control)
	_ = closeForwarder(forwarder)
}

func (runtime *Runtime) watchContext(ctx context.Context) {
	<-ctx.Done()
	_ = runtime.Close()
}
