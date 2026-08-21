//go:build linux

package trafficinspect

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectLinuxTrustBackend(t *testing.T) {
	tests := []struct {
		name string
		bins map[string]string
		want string
	}{
		{
			name: "Debian",
			bins: map[string]string{"update-ca-certificates": "/usr/sbin/update-ca-certificates"},
			want: "/usr/local/share/ca-certificates/kubeloop-traffic-inspection.crt",
		},
		{
			name: "RHEL",
			bins: map[string]string{"update-ca-trust": "/usr/bin/update-ca-trust"},
			want: "/etc/pki/ca-trust/source/anchors/kubeloop-traffic-inspection.pem",
		},
		{name: "unsupported", bins: map[string]string{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := detectLinuxTrustBackend(func(name string) (string, error) {
				if path := test.bins[name]; path != "" {
					return path, nil
				}
				return "", errors.New("not found")
			})
			if backend.anchorPath != test.want {
				t.Fatalf("anchor path = %q, want %q", backend.anchorPath, test.want)
			}
		})
	}
}

func TestLinuxTrustStoreInstallAndUninstallAreIdempotent(t *testing.T) {
	authority, err := LoadOrCreateAuthority(filepath.Join(t.TempDir(), authorityFileName))
	if err != nil {
		t.Fatal(err)
	}
	anchor := filepath.Join(t.TempDir(), "anchors", "kubeloop.crt")
	runner := &fakeLinuxTrustRunner{}
	store := &linuxTrustStore{
		runner: runner,
		isRoot: func() bool { return true },
		backend: linuxTrustBackend{
			anchorPath:    anchor,
			updateCommand: []string{"/usr/bin/true"},
		},
	}
	if err := store.Install(t.Context(), authority); err != nil {
		t.Fatal(err)
	}
	if err := store.Install(t.Context(), authority); err != nil {
		t.Fatal(err)
	}
	if err := store.Uninstall(t.Context(), authority); err != nil {
		t.Fatal(err)
	}
	if err := store.Uninstall(t.Context(), authority); err != nil {
		t.Fatal(err)
	}
	if runner.calls != 2 {
		t.Fatalf("privileged calls = %d, want 2", runner.calls)
	}
}

type fakeLinuxTrustRunner struct {
	calls int
}

func (r *fakeLinuxTrustRunner) CombinedOutput(_ context.Context, _ string, arguments ...string) ([]byte, error) {
	r.calls++
	if len(arguments) < 4 || arguments[0] != "-c" {
		return nil, errors.New("unexpected Linux trust command")
	}
	script := arguments[1]
	if strings.Contains(script, "install -m 0644") {
		if len(arguments) != 6 {
			return nil, errors.New("unexpected Linux install arguments")
		}
		if err := os.MkdirAll(arguments[3], 0o755); err != nil {
			return nil, err
		}
		content, err := os.ReadFile(arguments[4])
		if err != nil {
			return nil, err
		}
		return nil, os.WriteFile(arguments[5], content, 0o644)
	}
	if strings.Contains(script, "rm -f") {
		return nil, os.Remove(arguments[3])
	}
	return nil, errors.New("unexpected Linux trust script")
}
