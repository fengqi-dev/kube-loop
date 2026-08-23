package dataplane

import (
	"errors"
	"fmt"
	"net"

	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
)

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
