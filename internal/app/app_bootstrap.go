package app

import (
	goruntime "runtime"

	"github.com/fengqi-dev/kube-loop/internal/singbox/distribution"
)

func (a *App) Bootstrap() (BootstrapData, error) {
	profiles := a.serverProfiles()
	a.updateMu.RLock()
	updateState := a.updateState
	a.updateMu.RUnlock()
	return BootstrapData{
		Update: updateState, Platform: goruntime.GOOS,
		CoreVersion: distribution.Version, ServerProfiles: profiles,
	}, nil
}
