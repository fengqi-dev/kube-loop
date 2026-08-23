package dataplane

import (
	"context"
	"errors"

	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
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
			runtime.closeErr = errors.Join(runtime.closeErr, core.Close())
		}
		runtime.tunWG.Wait()
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
		runtime.closeErr = errors.Join(
			runtime.closeErr,
			ignoreClosed(runtime.bridge.Close()),
			closeConnection(control),
			closeForwarder(forwarder),
		)
		for _, streams := range draining {
			runtime.closeErr = errors.Join(
				runtime.closeErr,
				closeConnection(streams.control),
				closeForwarder(streams.forwarder),
			)
		}
		runtime.transportWG.Wait()
		close(runtime.done)
	})
	return runtime.closeErr
}

func (runtime *Runtime) watchContext(ctx context.Context) {
	<-ctx.Done()
	_ = runtime.Close()
}
