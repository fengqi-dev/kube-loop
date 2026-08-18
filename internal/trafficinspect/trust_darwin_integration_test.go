//go:build darwin && integration

package trafficinspect

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestSystemTrustStore_StatusIntegration(t *testing.T) {
	authority, err := LoadOrCreateAuthority(filepath.Join(t.TempDir(), authorityFileName))
	if err != nil {
		t.Fatalf("create temporary authority: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	status, err := NewSystemTrustStore().Status(ctx, authority)
	if err != nil {
		t.Fatalf("inspect macOS System Keychain: %v", err)
	}
	if status.Installed {
		t.Fatal("new temporary authority unexpectedly exists in macOS System Keychain")
	}
	if status.Store != systemKeychainPath {
		t.Fatalf("trust store = %q, want %q", status.Store, systemKeychainPath)
	}
}
