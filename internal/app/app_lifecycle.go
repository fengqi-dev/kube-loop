package app

import (
	"context"
	"fmt"
	"log"
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
		a.appendLog("INFO", "application startup initialized")
		watcher, err := powerwatch.New(powerwatch.Config{OnWake: func(event powerwatch.Event) {
			if a.dataPlanes == nil {
				return
			}
			profiles := a.dataPlanes.ResumeAll()
			a.appendLog("INFO", fmt.Sprintf(
				"System wake detected after %s; refreshing %d Data Plane profile(s)", event.SleptFor, profiles,
			))
		}})
		if err != nil {
			a.appendLog("ERROR", "Power wake monitor unavailable: "+err.Error())
		} else {
			watchContext, cancel := context.WithCancel(ctx)
			a.powerWatchCancel = cancel
			go watcher.Run(watchContext)
		}
		if a.mcp != nil {
			a.mcp.StartFromStore()
		}
		go func() {
			state := a.checkForUpdates(ctx)
			runtime.EventsEmit(ctx, "update:state", state)
		}()
	})
}

func (a *App) shutdown(context.Context) {
	shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if a.powerWatchCancel != nil {
		a.powerWatchCancel()
	}
	a.stopServerInventoryWatch("")
	if a.mcp != nil {
		if err := a.mcp.Stop(); err != nil {
			log.Printf("MCP shutdown: %v", err)
		}
	}
	if a.remoteFiles != nil {
		if err := a.remoteFiles.Shutdown(); err != nil {
			log.Printf("remote file transfer shutdown: %v", err)
		}
	}
	if a.remoteExecs != nil {
		if err := a.remoteExecs.Shutdown(); err != nil {
			log.Printf("remote Pod exec shutdown: %v", err)
		}
	}
	if a.remoteSSH != nil {
		if err := a.remoteSSH.Shutdown(); err != nil {
			log.Printf("remote Pod SSH shutdown: %v", err)
		}
	}
	if a.remoteForwards != nil {
		if err := a.remoteForwards.Shutdown(shutdownContext); err != nil {
			log.Printf("remote Port Forward shutdown: %v", err)
		}
	}
	if a.remoteExchanges != nil {
		if err := a.remoteExchanges.Shutdown(shutdownContext); err != nil {
			log.Printf("remote Exchange shutdown: %v", err)
		}
	}
	if a.remoteMirrors != nil {
		if err := a.remoteMirrors.Shutdown(shutdownContext); err != nil {
			log.Printf("remote Mirror shutdown: %v", err)
		}
	}
	if a.remotePreviews != nil {
		if err := a.remotePreviews.Shutdown(shutdownContext); err != nil {
			log.Printf("remote Preview shutdown: %v", err)
		}
	}
	if a.dataPlanes != nil {
		if err := a.dataPlanes.Shutdown(); err != nil {
			log.Printf("data plane shutdown: %v", err)
		}
	}
	if a.remoteSessions != nil {
		if err := a.remoteSessions.Shutdown(shutdownContext); err != nil {
			log.Printf("remote session shutdown: %v", err)
		}
	}
	if a.trafficInspectionOutput != nil {
		if err := a.trafficInspectionOutput.Close(); err != nil {
			log.Printf("traffic inspection output shutdown: %v", err)
		}
	}
}

func (a *App) context() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}

func (a *App) appendLog(level, message string) {
	log.Printf("[%s] %s", level, message)
}
