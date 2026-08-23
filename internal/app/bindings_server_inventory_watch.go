package app

import (
	"context"
	"slices"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	clientprofile "github.com/fengqi-dev/kube-loop/internal/client/profile"
	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
)

type ServerInventoryEvent struct {
	ProfileID string                          `json:"profileId"`
	Namespace string                          `json:"namespace"`
	Resource  clientremote.InventoryResource  `json:"resource"`
	Snapshot  *clientremote.InventorySnapshot `json:"snapshot,omitempty"`
	Error     string                          `json:"error,omitempty"`
}

func (a *App) startServerInventoryWatch(serverProfile clientprofile.Profile, namespace string, capabilities []string) {
	if a.remote == nil {
		return
	}
	a.inventoryWatchLifecycle.Lock()
	defer a.inventoryWatchLifecycle.Unlock()
	a.stopServerInventoryWatchLocked("")
	ctx, cancel := context.WithCancel(a.context())
	a.inventoryWatchMu.Lock()
	a.inventoryWatchProfile = serverProfile.ID
	a.inventoryWatchCancel = cancel
	a.inventoryWatchMu.Unlock()
	if slices.Contains(capabilities, "pods.watch") {
		a.inventoryWatchWG.Go(func() {
			a.runServerInventoryWatch(ctx, serverProfile, namespace, clientremote.InventoryPods)
		})
	}
	if slices.Contains(capabilities, "services.watch") {
		a.inventoryWatchWG.Go(func() {
			a.runServerInventoryWatch(ctx, serverProfile, namespace, clientremote.InventoryServices)
		})
	}
}

func (a *App) runServerInventoryWatch(
	ctx context.Context,
	serverProfile clientprofile.Profile,
	namespace string,
	resource clientremote.InventoryResource,
) {
	for ctx.Err() == nil {
		watch, err := a.remote.OpenInventoryWatch(ctx, serverProfile, namespace, resource)
		if err == nil {
			for ctx.Err() == nil {
				snapshot, nextErr := watch.Next(ctx)
				if nextErr != nil {
					err = nextErr
					break
				}
				a.emitServerInventoryEvent(ServerInventoryEvent{
					ProfileID: serverProfile.ID, Namespace: namespace, Resource: resource, Snapshot: &snapshot,
				})
			}
			_ = watch.Close()
		}
		if ctx.Err() != nil {
			return
		}
		a.emitServerInventoryEvent(ServerInventoryEvent{
			ProfileID: serverProfile.ID, Namespace: namespace, Resource: resource, Error: err.Error(),
		})
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (a *App) emitServerInventoryEvent(event ServerInventoryEvent) {
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "server-inventory:snapshot", event)
	}
}

func (a *App) stopServerInventoryWatch(profileID string) {
	a.inventoryWatchLifecycle.Lock()
	defer a.inventoryWatchLifecycle.Unlock()
	a.stopServerInventoryWatchLocked(profileID)
}

func (a *App) stopServerInventoryWatchLocked(profileID string) {
	a.inventoryWatchMu.Lock()
	if profileID != "" && a.inventoryWatchProfile != profileID {
		a.inventoryWatchMu.Unlock()
		return
	}
	cancel := a.inventoryWatchCancel
	a.inventoryWatchCancel = nil
	a.inventoryWatchProfile = ""
	a.inventoryWatchMu.Unlock()
	if cancel != nil {
		cancel()
	}
	a.inventoryWatchWG.Wait()
}
