package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/fengqi-dev/kube-loop/internal/update"
)

func (a *App) CheckForUpdates() update.Info {
	a.appendLog("INFO", "checking for application updates")
	checkContext, cancel := context.WithTimeout(a.context(), 20*time.Second)
	defer cancel()
	state := a.checkForUpdates(checkContext)
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "update:state", state)
	}
	return state
}

func (a *App) OpenUpdatePage() error {
	a.updateMu.RLock()
	target := a.updateState.URL
	a.updateMu.RUnlock()
	if target == "" {
		target = releaseURL
	}
	if a.ctx == nil {
		err := errors.New("application is not ready")
		a.appendLog("ERROR", "open update page: "+err.Error())
		return err
	}
	a.appendLog("INFO", "opening application update page")
	runtime.BrowserOpenURL(a.ctx, target)
	return nil
}

func (a *App) checkForUpdates(ctx context.Context) update.Info {
	a.updateCheck.Lock()
	defer a.updateCheck.Unlock()
	state, err := a.updater.Check(ctx)
	switch {
	case err != nil:
		state.Error = err.Error()
		a.appendLog("WARN", fmt.Sprintf("application update check failed: %v", err))
	case state.Available:
		a.appendLog("INFO", fmt.Sprintf(
			"application update available: current=%s latest=%s",
			state.CurrentVersion, state.LatestVersion,
		))
	default:
		a.appendLog("INFO", "application is up to date")
	}
	a.updateMu.Lock()
	a.updateState = state
	a.updateMu.Unlock()
	return state
}
