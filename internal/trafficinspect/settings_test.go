package trafficinspect

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSettingsStoreUsesFallbackAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings", "traffic-inspection.json")
	store, err := NewSettingsStore(path)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := store.Load(Settings{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if !settings.Enabled {
		t.Fatal("missing settings did not use enabled fallback")
	}
	if err := store.Save(Settings{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	settings, err = store.Load(Settings{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if settings.Enabled {
		t.Fatal("persisted disabled setting was ignored")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("settings mode = %o", info.Mode().Perm())
	}
}

func TestSettingsStoreRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "traffic-inspection.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"enabled":true,"extra":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewSettingsStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(Settings{}); err == nil {
		t.Fatal("invalid settings were accepted")
	}
}
