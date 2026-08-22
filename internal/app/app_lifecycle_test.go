package app

import (
	"context"
	"testing"
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
