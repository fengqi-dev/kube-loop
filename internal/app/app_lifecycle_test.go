package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestLifecycleAdaptersWithoutRuntimeContext(t *testing.T) {
	application := &App{}
	if StartupHandler(application) == nil {
		t.Fatal("StartupHandler returned nil")
	}
	shutdown := ShutdownHandler(application)
	if shutdown == nil {
		t.Fatal("ShutdownHandler returned nil")
	}

	ShowWindow(application)
	Quit(application)
	shutdown(t.Context())
}

func TestAppContext(t *testing.T) {
	application := &App{}
	if application.context() == nil {
		t.Fatal("context() returned nil without a runtime context")
	}

	type contextKey struct{}
	runtimeContext := context.WithValue(t.Context(), contextKey{}, "runtime")
	application.ctx = runtimeContext
	if got := application.context(); got != runtimeContext {
		t.Fatalf("context() = %v, want runtime context", got)
	}
}

func TestShutdownWaitsForBackgroundCleanup(t *testing.T) {
	application := &App{}
	ctx, cancel := context.WithCancel(t.Context())
	application.backgroundCancel = cancel
	cleanupStarted := make(chan struct{})
	releaseCleanup := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseCleanup) }) }
	t.Cleanup(release)
	application.backgroundWG.Go(func() {
		<-ctx.Done()
		close(cleanupStarted)
		<-releaseCleanup
	})
	stopped := make(chan struct{})
	go func() {
		application.shutdown(t.Context())
		close(stopped)
	}()
	select {
	case <-cleanupStarted:
	case <-time.After(time.Second):
		t.Fatal("background task did not observe shutdown cancellation")
	}
	select {
	case <-stopped:
		t.Fatal("shutdown returned before background cleanup")
	case <-time.After(100 * time.Millisecond):
	}
	release()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not return after background cleanup")
	}
}

func TestShutdownBoundsBackgroundCleanupWait(t *testing.T) {
	application := &App{shutdownTimeout: 20 * time.Millisecond}
	ctx, cancel := context.WithCancel(t.Context())
	application.backgroundCancel = cancel
	releaseCleanup := make(chan struct{})
	t.Cleanup(func() { close(releaseCleanup) })
	application.backgroundWG.Go(func() {
		<-ctx.Done()
		<-releaseCleanup
	})
	stopped := make(chan struct{})
	go func() {
		application.shutdown(t.Context())
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("shutdown ignored the background cleanup deadline")
	}
}

func TestRunShutdownActionReturnsAndHonorsDeadline(t *testing.T) {
	t.Run("completed", func(t *testing.T) {
		runShutdownAction(t.Context(), "test", func() error {
			return errors.New("close failed")
		})
	})

	t.Run("deadline", func(t *testing.T) {
		release := make(chan struct{})
		t.Cleanup(func() { close(release) })
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
		defer cancel()
		stopped := make(chan struct{})
		go func() {
			runShutdownAction(ctx, "test", func() error {
				<-release
				return nil
			})
			close(stopped)
		}()
		select {
		case <-stopped:
		case <-time.After(time.Second):
			t.Fatal("shutdown action ignored its deadline")
		}
	})
}
