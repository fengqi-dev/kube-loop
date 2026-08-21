package trafficinspect

import (
	"path/filepath"
	"testing"
)

func TestDefaultPathsUseStructuredLayout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	authority, err := DefaultAuthorityPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".kubeloop", "secrets", "inspection-ca.pem"); authority != want {
		t.Fatalf("authority path = %q, want %q", authority, want)
	}
	settings, err := NewSettingsStore("")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".kubeloop", "config", "traffic-inspection.json"); settings.path != want {
		t.Fatalf("settings path = %q, want %q", settings.path, want)
	}
	protobuf, err := NewProtobufSchemaStore("", NewProtobufDecoder())
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".kubeloop", "data", "traffic-inspection", "protobuf.json"); protobuf.path != want {
		t.Fatalf("protobuf path = %q, want %q", protobuf.path, want)
	}
}
