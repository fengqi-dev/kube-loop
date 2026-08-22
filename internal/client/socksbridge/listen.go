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
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", listenAddress)
	if err != nil {
		return nil, err
	}
	server := &Server{GatewayAddress: gatewayAddress, SessionToken: token}
	if config.inspectorFactory != nil {
		server.inspector, err = config.inspectorFactory(server.dial)
		if err != nil {
			_ = listener.Close()
			return nil, fmt.Errorf("create SOCKS TCP inspector: %w", err)
		}
	}
	go func() {
		<-ctx.Done()
		_ = listener.Close() // Closing only wakes Serve after cancellation; no caller can act on the result.
	}()
	go func() { _ = server.Serve(listener) }()
	return &Bridge{Listener: listener, server: server}, nil
}
