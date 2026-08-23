package app

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestStopServerInventoryWatchWaitsForReaderCleanup(t *testing.T) {
	app := &App{}
	ctx, cancel := context.WithCancel(t.Context())
	app.inventoryWatchProfile = "server"
	app.inventoryWatchCancel = cancel
	cleanupStarted := make(chan struct{})
	releaseCleanup := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseCleanup) }) }
	t.Cleanup(release)
	app.inventoryWatchWG.Go(func() {
		<-ctx.Done()
		close(cleanupStarted)
		<-releaseCleanup
	})
	stopped := make(chan struct{})
	go func() {
		app.stopServerInventoryWatch("server")
		close(stopped)
	}()
	select {
	case <-cleanupStarted:
	case <-time.After(time.Second):
		t.Fatal("Inventory Watch reader did not observe cancellation")
	}
	select {
	case <-stopped:
		t.Fatal("stop returned before Inventory Watch reader cleanup")
	case <-time.After(100 * time.Millisecond):
	}
	release()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("stop did not return after Inventory Watch reader cleanup")
	}
	if app.inventoryWatchProfile != "" || app.inventoryWatchCancel != nil {
		t.Fatal("Inventory Watch state was not cleared")
	}
}
