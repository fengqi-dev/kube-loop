package dataplane

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
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
	"github.com/fengqi-dev/kube-loop/internal/trafficinspect"
)

func Start(
	ctx context.Context,
	serverProfile profile.Profile,
	session remote.Session,
	ticketSource func(context.Context) (remote.RelayTicket, error),
	config Config,
) (*Runtime, error) {
	if ctx == nil {
		return nil, errors.New("data Plane context is required")
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
			State: dataplaneConnected, Mode: ModeSOCKS, SessionID: session.ID, SessionGeneration: session.Generation,
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
		return nil, errors.New("traffic stream context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	runtime.transportMu.Lock()
	if runtime.ctx.Err() != nil || runtime.forwarder == nil || runtime.token == (tunnel.SessionToken{}) ||
		signalClosed(runtime.transportDone) {
		runtime.transportMu.Unlock()
		return nil, errors.New("data Plane transport is not connected")
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
	if err := tunnel.WriteTrafficOpen(
		connection,
		tunnel.TrafficOpenRequest{Mode: mode, TaskID: taskID},
		token,
	); err != nil {
		return nil, fmt.Errorf("open Traffic Task stream: %w", contextConnectionError(ctx, err))
	}
	if err := tunnel.ReadStatus(connection); err != nil {
		return nil, fmt.Errorf("start Traffic Task stream: %w", contextConnectionError(ctx, err))
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	runtime.transportMu.Lock()
	runtimeActive := runtime.ctx.Err() == nil
	transportMatches := runtime.forwarder == forwarder && runtime.control == control && runtime.token == token
	current := runtimeActive && transportMatches && runtime.transportDone == transportDone &&
		!signalClosed(transportDone)
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
		return nil, errors.New("data Plane transport changed while opening Traffic Task stream")
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
		return openedTransport{}, errors.New("relayTicket source is required")
	}
	if strings.TrimSpace(serverProfile.ID) == "" || session.State != dataplaneSessionActive {
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
		return openedTransport{}, errors.New("data Plane NetworkSpec hash does not match the Session")
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
		return "", errors.New("server Profile URL is invalid")
	}
	endpoint, err := url.Parse(assignedEndpoint)
	if err != nil || (endpoint.Scheme != "ws" && endpoint.Scheme != dataplaneWSSScheme) || endpoint.Host == "" {
		return "", errors.New("relay assignment endpoint is invalid")
	}
	if base.Scheme == "http" {
		endpoint.Scheme = "ws"
	} else {
		endpoint.Scheme = dataplaneWSSScheme
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
			if ticket.Endpoint != initial.Endpoint || ticket.RelayID != initial.RelayID ||
				ticket.DeviceID != initial.DeviceID {
				return "", errors.New("relay assignment changed while opening a WebSocket pool")
			}
		}
		if ticket.Ticket == "" || strings.TrimSpace(ticket.Ticket) != ticket.Ticket {
			return "", errors.New("relayTicket source returned an invalid ticket")
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
		tunnelPath = defaultTunnelPath
	}
	parsedPath, err := url.ParseRequestURI(tunnelPath)
	if err != nil || !strings.HasPrefix(tunnelPath, "/") || parsedPath.IsAbs() || parsedPath.Host != "" ||
		parsedPath.RawQuery != "" || parsedPath.Fragment != "" || parsedPath.EscapedPath() != tunnelPath ||
		strings.Contains(
			tunnelPath,
			"//",
		) || strings.Contains(tunnelPath, "/./") || strings.Contains(tunnelPath, "/../") ||
		strings.HasSuffix(tunnelPath, "/.") || strings.HasSuffix(tunnelPath, "/..") {
		return "", errors.New("server Profile tunnel path is invalid")
	}
	endpoint, err := url.Parse(baseURL)
	if err != nil {
		return "", errors.New("server Profile URL is invalid")
	}
	switch endpoint.Scheme {
	case "https":
		endpoint.Scheme = dataplaneWSSScheme
	case "http":
		endpoint.Scheme = "ws"
	default:
		return "", errors.New("server Profile URL must use HTTP or HTTPS")
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
				socksbridge.WithTCPInspector(
					func(dialContext socksbridge.DialContextFunc) (socksbridge.TCPInspector, error) {
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
							Protobuf:    inspection.Protobuf,
							OnSinkError: inspection.OnSinkError,
							TLSConfig:   inspection.TLSConfig,
							AllowHTTP2:  true,
						})
					},
				),
			)
		}
	}
	if config.dialContext == nil {
		config.dialContext = (&net.Dialer{}).DialContext
	}
	return config
}
