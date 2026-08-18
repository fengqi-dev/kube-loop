//go:build windows

package trafficinspect

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsTrustStoreInstallAndUninstallAreIdempotent(t *testing.T) {
	authority, err := LoadOrCreateAuthority(filepath.Join(t.TempDir(), authorityFileName))
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeWindowsTrustRunner{}
	store := &windowsTrustStore{runner: runner}
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
	if runner.installCalls != 1 || runner.uninstallCalls != 1 {
		t.Fatalf("install/uninstall calls = %d/%d, want 1/1", runner.installCalls, runner.uninstallCalls)
	}
	if !containsCertificate(runner.publicPEM, authority.certificate.Leaf.Raw) {
		t.Fatal("Windows trust install did not receive the expected public certificate")
	}
}

type fakeWindowsTrustRunner struct {
	installed      bool
	installCalls   int
	uninstallCalls int
	publicPEM      []byte
}

func (r *fakeWindowsTrustRunner) CombinedOutput(_ context.Context, name string, arguments ...string) ([]byte, error) {
	if name != "powershell.exe" || len(arguments) < 4 {
		return nil, errors.New("unexpected Windows trust command")
	}
	switch arguments[3] {
	case windowsFindCertificateScript:
		if r.installed {
			return []byte("ABCDEF\r\n"), nil
		}
		return nil, nil
	case windowsInstallCertificateScript:
		if len(arguments) != 6 {
			return nil, errors.New("unexpected Windows install arguments")
		}
		content, err := os.ReadFile(arguments[5])
		if err != nil {
			return nil, err
		}
		r.publicPEM = content
		r.installCalls++
		r.installed = true
		return nil, nil
	case windowsUninstallCertificateScript:
		r.uninstallCalls++
		r.installed = false
		return nil, nil
	default:
		return nil, errors.New("unexpected Windows trust script")
	}
}
