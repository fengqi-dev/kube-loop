package dataplane

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
	"github.com/fengqi-dev/kube-loop/internal/singbox"
)

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
		runtime.status.Mode = ModeSOCKS
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
		err = errors.New("tUN stopped unexpectedly")
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

func (runtime *Runtime) watchContext(ctx context.Context) {
	<-ctx.Done()
	_ = runtime.Close()
}
