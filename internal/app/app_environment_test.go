package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/trafficinspect"
)

func TestNewAppUsesExplicitProfilePath(t *testing.T) {
	profilePath := filepath.Join(t.TempDir(), "isolated", "servers.json")
	t.Setenv("KUBELOOP_PROFILE_PATH", "  "+profilePath+"  ")

	application := NewApp("dev", nil)
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
	store, err := trafficinspect.NewSettingsStore(filepath.Join(directory, "traffic-inspection.json"))
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
	profilePath := filepath.Join(t.TempDir(), "traffic", "servers.json")
	config, events, switchable := newTrafficInspection(profilePath)
	if !config.Enabled || switchable == nil || !switchable.Enabled() {
		t.Fatalf("traffic inspection default = %#v, switch = %#v", config, switchable)
	}
	if !config.Policy.CaptureBodies || config.Policy.MaxBodyBytes != 4<<20 {
		t.Fatalf("capture policy = %#v", config.Policy)
	}
	if events == nil || config.IsEnabled == nil {
		t.Fatal("dynamic traffic inspection dependencies are missing")
	}
	if closer, ok := config.Sink.(interface{ Close() error }); ok {
		t.Cleanup(func() { _ = closer.Close() })
	}
}

func TestTrafficInspectionWritesToProfileDirectoryWhenEnabled(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "traffic")
	profilePath := filepath.Join(directory, "servers.json")
	path := filepath.Join(directory, "traffic-inspection.jsonl")
	config, events, switchable := newTrafficInspection(profilePath)
	if config.Sink == nil {
		t.Fatal("traffic inspection sink is nil")
	}
	if events == nil {
		t.Fatal("traffic inspection event buffer is nil")
	}
	switchable.SetEnabled(true)
	event := trafficinspect.Event{SchemaVersion: trafficinspect.EventSchemaVersion, ID: "event-1"}
	if err := config.Sink.Emit(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	buffered := events.Snapshot()
	if len(buffered) != 1 || buffered[0].ID != event.ID {
		t.Fatalf("buffered events = %#v", buffered)
	}
	closer, ok := config.Sink.(interface{ Close() error })
	if !ok {
		t.Fatal("traffic inspection file sink is not closable")
	}
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded trafficinspect.Event
	if err := json.Unmarshal(contents, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ID != event.ID {
		t.Fatalf("event ID = %q", decoded.ID)
	}
}
