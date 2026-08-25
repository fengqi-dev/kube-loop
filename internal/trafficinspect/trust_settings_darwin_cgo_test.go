//go:build darwin && cgo

package trafficinspect

import (
	"path/filepath"
	"testing"
)

func TestDarwinNativeTrustSettingsReportsNewAuthorityUntrusted(t *testing.T) {
	authority, err := LoadOrCreateAuthority(filepath.Join(t.TempDir(), authorityFileName))
	if err != nil {
		t.Fatalf("create authority: %v", err)
	}
	installed, err := newDarwinTrustSettings().Installed(authority)
	if err != nil {
		t.Fatalf("inspect admin trust settings: %v", err)
	}
	if installed {
		t.Fatal("new authority unexpectedly has admin trust settings")
	}
}
