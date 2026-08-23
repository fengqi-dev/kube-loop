package dataplane

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
)

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
	staleGeneration := session.Generation < runtime.session.Generation
	if session.State != dataplaneSessionActive || session.ID != runtime.session.ID || staleGeneration {
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
		return errors.New("data Plane runtime is not awaiting recovery")
	}
	restoreTUN := networkChanged && runtime.tun != nil
	if restoreTUN {
		core := runtime.tun
		cancelTUN := runtime.tunCancel
		runtime.tun = nil
		runtime.tunCancel = nil
		runtime.status.Mode = ModeSOCKS
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
	runtime.status.State = dataplaneConnected
	if restoreTUN {
		if _, err := runtime.startTUNLocked(ctx); err != nil {
			runtime.transportMu.Unlock()
			runtime.stateMu.Unlock()
			_ = transport.control.Close()
			_ = transport.forwarder.Close()
			return fmt.Errorf("restore TUN for refreshed NetworkSpec: %w", err)
		}
	}
	runtime.transportWG.Go(func() {
		runtime.watchControl(transport.control, transportDone)
	})
	runtime.transportMu.Unlock()
	runtime.stateMu.Unlock()
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
