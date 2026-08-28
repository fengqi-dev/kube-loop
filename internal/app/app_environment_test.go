package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/trafficinspect"
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

func TestNewAppLoadsPersistedTrafficInspectionSetting(t *testing.T) {
	directory := t.TempDir()
	profilePath := filepath.Join(directory, "servers.json")
	store, err := trafficinspect.NewSettingsStore(filepath.Join(directory, "config", "traffic-inspection.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(trafficinspect.Settings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBELOOP_PROFILE_PATH", profilePath)
	application := NewApp("dev", nil)
	t.Cleanup(func() { application.shutdown(context.Background()) })
	if settings := application.GetTrafficInspectionSettings(); !settings.Enabled {
		t.Fatalf("traffic inspection settings = %#v", settings)
	}
}

func TestTrafficInspectionDefaultsEnabledWithoutEnvironmentConfiguration(t *testing.T) {
	config, enabled := newTrafficInspection()
	if !config.Enabled || enabled == nil || !enabled.Load() {
		t.Fatalf("traffic inspection default = %#v, enabled = %#v", config, enabled)
	}
	if !config.Policy.CaptureBodies || config.Policy.MaxBodyBytes != 4<<20 {
		t.Fatalf("capture policy = %#v", config.Policy)
	}
	if config.IsEnabled == nil {
		t.Fatal("dynamic traffic inspection dependencies are missing")
	}
}
