package dataplane

import (
	"context"
	"errors"

	"github.com/fengqi-dev/kube-loop/internal/singbox"
)

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
	runtime.cancel()
}
