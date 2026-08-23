package app

import (
	"context"
	"path/filepath"
	"testing"
)

func TestNewAppConfiguresRemoteRuntime(t *testing.T) {
	profilePath := filepath.Join(t.TempDir(), "servers.json")
	t.Setenv("KUBELOOP_PROFILE_PATH", profilePath)

	application := NewApp("dev", nil)
	t.Cleanup(func() { application.shutdown(context.Background()) })

	if application.auth == nil || application.remote == nil || application.remoteSessions == nil ||
		application.dataPlanes == nil || application.remoteFiles == nil || application.remoteExecs == nil {
		t.Fatalf("remote runtime is incomplete: %#v", application)
	}
}
