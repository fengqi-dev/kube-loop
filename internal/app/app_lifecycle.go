package app

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/fengqi-dev/kube-loop/internal/client/powerwatch"
)

func StartupHandler(a *App) func(context.Context) { return a.startup }

func ShutdownHandler(a *App) func(context.Context) { return a.shutdown }

func ShowWindow(a *App) {
	if a.ctx == nil {
		return
	}
	runtime.WindowUnminimise(a.ctx)
	runtime.WindowShow(a.ctx)
}

func Quit(a *App) {
	if a.ctx == nil {
		return
	}
	runtime.Quit(a.ctx)
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.once.Do(func() {
		a.logInfo("application startup initialized")
		if a.startupTUNCleanup != nil {
			cleanupContext, cancelCleanup := context.WithTimeout(ctx, 5*time.Second)
			if err := a.startupTUNCleanup(cleanupContext); err != nil {
				a.logWarn("Stale privileged TUN cleanup failed: " + err.Error())
			} else {
				a.logInfo("Stale privileged TUN sessions cleaned")
			}
			cancelCleanup()
		}
		backgroundContext, cancelBackground := context.WithCancel(ctx)
		a.backgroundCancel = cancelBackground
		watcher, err := powerwatch.New(powerwatch.Config{OnWake: func(event powerwatch.Event) {
			if a.dataPlanes == nil {
				return
			}
			profiles := a.dataPlanes.ResumeAll()
			a.logInfo(fmt.Sprintf(
				"System wake detected after %s; refreshing %d Data Plane profile(s)", event.SleptFor, profiles,
			))
		}})
		if err != nil {
			a.logError("Power wake monitor unavailable: " + err.Error())
		} else {
			a.backgroundWG.Go(func() {
				watcher.Run(backgroundContext)
			})
		}
		if a.mcp != nil {
			a.mcp.StartFromStore()
		}
		syncSessions := a.startupSessionSync
		if syncSessions == nil {
			syncSessions = a.syncServerSessions
		}
		a.backgroundWG.Go(func() {
			if err := syncSessions(backgroundContext); err != nil {
				a.logWarn("Session synchronization failed: " + err.Error())
				return
			}
			a.logInfo("Sessions synchronized from TrafficBindings")
		})
		if a.updater != nil {
			a.backgroundWG.Go(func() {
				state := a.checkForUpdates(backgroundContext)
				runtime.EventsEmit(backgroundContext, "update:state", state)
			})
		}
	})
}

func (a *App) syncServerSessions(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if a.profiles == nil || a.remoteSessions == nil {
		return nil
	}
	state := a.profiles.Snapshot()
	for _, serverProfile := range state.Profiles {
		if serverProfile.ID != state.ActiveProfileID {
			continue
		}
		_, err := a.LoadServerInventory(serverProfile.ID, serverProfile.LastNamespace)
		return err
	}
	return nil
}

func (a *App) shutdown(ctx context.Context) {
	shutdownTimeout := a.shutdownTimeout
	if shutdownTimeout <= 0 {
		shutdownTimeout = 5 * time.Second
	}
	shutdownContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if a.backgroundCancel != nil {
		a.backgroundCancel()
	}
	if err := waitForBackgroundShutdown(shutdownContext, &a.backgroundWG); err != nil {
		log.Printf("application background shutdown: %v", err)
	}
	runShutdownAction(shutdownContext, "Server inventory Watch", func() error {
		a.stopServerInventoryWatch("")
		return nil
	})
	if a.mcp != nil {
		runShutdownAction(shutdownContext, "MCP", a.mcp.Stop)
	}
	if a.remoteFiles != nil {
		runShutdownAction(shutdownContext, "remote file transfer", a.remoteFiles.Shutdown)
	}
	if a.remoteExecs != nil {
		runShutdownAction(shutdownContext, "remote Pod exec", a.remoteExecs.Shutdown)
	}
	if a.remoteSSH != nil {
		runShutdownAction(shutdownContext, "remote Pod SSH", a.remoteSSH.Shutdown)
	}
	if a.remoteForwards != nil {
		runShutdownAction(shutdownContext, "remote Port Forward", func() error {
			return a.remoteForwards.Shutdown(shutdownContext)
		})
	}
	if a.remoteExchanges != nil {
		runShutdownAction(shutdownContext, "remote Exchange", func() error {
			return a.remoteExchanges.Shutdown(shutdownContext)
		})
	}
	if a.remoteMirrors != nil {
		runShutdownAction(shutdownContext, "remote Mirror", func() error {
			return a.remoteMirrors.Shutdown(shutdownContext)
		})
	}
	if a.remotePreviews != nil {
		runShutdownAction(shutdownContext, "remote Preview", func() error {
			return a.remotePreviews.Shutdown(shutdownContext)
		})
	}
	if a.dataPlanes != nil {
		runShutdownAction(shutdownContext, "Data Plane", a.dataPlanes.Shutdown)
	}
	if a.remoteSessions != nil {
		runShutdownAction(shutdownContext, "remote Session", func() error {
			return a.remoteSessions.Shutdown(shutdownContext)
		})
	}
}

func runShutdownAction(ctx context.Context, name string, action func() error) {
	result := make(chan error, 1)
	go func() { result <- action() }()
	select {
	case err := <-result:
		if err != nil {
			log.Printf("%s shutdown: %v", name, err)
		}
	case <-ctx.Done():
		log.Printf("%s shutdown: %v", name, ctx.Err())
	}
}

func waitForBackgroundShutdown(ctx context.Context, wait *sync.WaitGroup) error {
	done := make(chan struct{})
	go func() {
		wait.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *App) context() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}
