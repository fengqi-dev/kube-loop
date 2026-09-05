package socksbridge

import (
	"context"
	"net"
)

func Listen(ctx context.Context, listenAddress string) (*Bridge, error) {
	baseListener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", listenAddress)
	if err != nil {
		return nil, err
	}
	listener := newTrackedListener(baseListener)
	server := &Server{tasks: newGoroutinePool()}
	serveDone := make(chan struct{})
	bridge := &Bridge{Listener: listener, server: server, serveDone: serveDone}
	bridge.stopContext = context.AfterFunc(ctx, func() { _ = bridge.Close() })
	go func() {
		defer close(serveDone)
		_ = server.Serve(listener)
	}()
	return bridge, nil
}
