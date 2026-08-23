package socksbridge

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
)

func Listen(
	ctx context.Context,
	gatewayAddress, listenAddress string,
	token tunnel.SessionToken,
	options ...ListenOption,
) (*Bridge, error) {
	config := listenConfig{}
	for _, option := range options {
		if option == nil {
			return nil, errors.New("nil SOCKS bridge option")
		}
		if err := option(&config); err != nil {
			return nil, err
		}
	}
	baseListener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", listenAddress)
	if err != nil {
		return nil, err
	}
	listener := newTrackedListener(baseListener)
	server := &Server{
		GatewayAddress: gatewayAddress,
		SessionToken:   token,
		tasks:          newGoroutinePool(),
	}
	if config.inspectorFactory != nil {
		server.inspector, err = config.inspectorFactory(server.dial)
		if err != nil {
			_ = listener.Close()
			return nil, fmt.Errorf("create SOCKS TCP inspector: %w", err)
		}
	}
	serveDone := make(chan struct{})
	bridge := &Bridge{Listener: listener, server: server, serveDone: serveDone}
	bridge.stopContext = context.AfterFunc(ctx, func() { _ = bridge.Close() })
	go func() {
		defer close(serveDone)
		_ = server.Serve(listener)
	}()
	return bridge, nil
}
