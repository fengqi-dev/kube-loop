package dataplane

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
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
