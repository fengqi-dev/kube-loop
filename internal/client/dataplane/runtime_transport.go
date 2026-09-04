package dataplane

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/client/websocketmux"
	"github.com/fengqi-dev/kube-loop/internal/middleware"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
	"github.com/fengqi-dev/kube-loop/internal/transport/trafficstream"
)

func Start(
	ctx context.Context,
	serverProfile profile.Profile,
	session remote.Session,
	ticketSource func(context.Context) (remote.RelayTicket, error),
	config Config,
) (*Runtime, error) {
	return startWithLifetime(ctx, ctx, serverProfile, session, ticketSource, config)
}

func startWithLifetime(
	startupCtx context.Context,
	lifetimeCtx context.Context,
	serverProfile profile.Profile,
	session remote.Session,
	ticketSource func(context.Context) (remote.RelayTicket, error),
	config Config,
) (*Runtime, error) {
	if startupCtx == nil || lifetimeCtx == nil {
		return nil, errors.New("data Plane context is required")
	}
	useDefaultListenAddress := strings.TrimSpace(config.ListenAddress) == ""
	config = normalizedConfig(config)
	runtimeCtx, cancel := context.WithCancel(lifetimeCtx)
	stopStartupCancel := context.AfterFunc(startupCtx, cancel)
	transport, err := openTransport(runtimeCtx, serverProfile, session, ticketSource, config)
	if err != nil {
		stopStartupCancel()
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
		stopStartupCancel()
		cancel()
		return nil, fmt.Errorf("start Data Plane SOCKS bridge: %w", err)
	}
	stopStartupCancel()
	if err := startupCtx.Err(); err != nil {
		_ = bridge.Close()
		_ = transport.control.Close()
		_ = transport.forwarder.Close()
		cancel()
		return nil, err
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
	runtime.trafficEncryption = transport.trafficEncryption
	runtime.trafficEncryptionSet = true
	runtime.noisePublicKey = append([]byte(nil), transport.noisePublicKey...)
	bridge.SetLogHandler(runtime.appendSOCKSLog)
	runtime.appendSOCKSLog("listening on " + bridge.Addr().String())
	runtime.startControlWatch(transport.control, runtime.transportDone)
	go runtime.watchContext(runtimeCtx)
	return runtime, nil
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
	ctx, _ = middleware.Ensure(ctx)
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
	clientEncryption := ticket.TrafficEncryption != nil && *ticket.TrafficEncryption
	if config.TrafficEncryption != nil && *config.TrafficEncryption != clientEncryption {
		return openedTransport{}, errors.New("client and Control Plane traffic encryption settings do not match")
	}
	var noisePublicKey []byte
	if clientEncryption {
		noisePublicKey, err = trafficstream.DecodeNoisePublicKey(ticket.NoisePublicKey)
		if err != nil {
			return openedTransport{}, errors.New("control plane returned an invalid Gateway Noise public key")
		}
	} else if !clientEncryption && ticket.NoisePublicKey != "" {
		return openedTransport{}, errors.New("control plane returned a Noise key for plaintext traffic")
	}
	webSocketURL, err := transportURL(serverProfile, ticket.Endpoint)
	if err != nil {
		return openedTransport{}, err
	}
	boundSource := newAssignmentTokenSource(ticketSource, ticket)
	forwarder, err := config.startForwarder(ctx, websocketmux.ClientConfig{
		URL: webSocketURL, TokenSource: boundSource,
		TLSConfig: config.TLSConfig, ClientVersion: config.ClientVersion, DeviceID: ticket.DeviceID,
		SessionID: session.ID, SessionGeneration: session.Generation, Logger: config.Logger,
		TrafficEncryption: &clientEncryption,
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
	return openedTransport{
		forwarder: forwarder, control: control, token: token,
		trafficEncryption: clientEncryption,
		noisePublicKey:    noisePublicKey,
	}, nil
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

func (runtime *Runtime) startControlWatch(control net.Conn, transportDone chan struct{}) {
	runtime.transportWG.Go(func() {
		runtime.watchControl(control, transportDone)
	})
}

func (runtime *Runtime) watchControl(control net.Conn, transportDone chan struct{}) {
	var buffer [1]byte
	_, err := control.Read(buffer[:])
	if runtime.ctx.Err() != nil {
		return
	}
	if err == nil {
		err = errors.New("data Plane control stream returned unexpected data")
	} else {
		err = fmt.Errorf("data Plane control stream closed: %w", err)
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
		reason = errors.New("data Plane transport refresh requested")
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
