package app

import (
	"context"
	"path/filepath"
	"testing"
)

func TestNewAppUsesExplicitProfilePath(t *testing.T) {
	profilePath := filepath.Join(t.TempDir(), "isolated", "servers.json")
	t.Setenv("KUBELOOP_PROFILE_PATH", "  "+profilePath+"  ")

	application := NewApp("dev", nil)
	t.Cleanup(func() { application.shutdown(context.Background()) })
	if application.profiles == nil {
		t.Fatal("profile store is nil")
	}
	if got := application.profiles.Path(); got != profilePath {
		t.Fatalf("profile path = %q, want %q", got, profilePath)
	}
}
