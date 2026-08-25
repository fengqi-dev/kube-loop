//go:build darwin

package trafficinspect

import (
	"context"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestDarwinTrustStore_InstallAndUninstallAreIdempotent(t *testing.T) {
	authority, err := LoadOrCreateAuthority(filepath.Join(t.TempDir(), authorityFileName))
	if err != nil {
		t.Fatalf("create authority: %v", err)
	}
	runner := &fakeDarwinTrustRunner{authority: authority}
	store := &darwinTrustStore{runner: runner}

	status, err := store.Status(t.Context(), authority)
	if err != nil {
		t.Fatalf("read initial trust status: %v", err)
	}
	if status.Installed {
		t.Fatal("new authority unexpectedly reported as installed")
	}
	if status.FingerprintSHA256 != authority.FingerprintSHA256() || status.Store != systemKeychainPath {
		t.Fatalf("unexpected trust status: %#v", status)
	}

	if err := store.Install(t.Context(), authority); err != nil {
		t.Fatalf("install authority: %v", err)
	}
	if err := store.Install(t.Context(), authority); err != nil {
		t.Fatalf("repeat authority install: %v", err)
	}
	if runner.installCalls != 1 {
		t.Fatalf("install calls = %d, want 1", runner.installCalls)
	}
	if runner.installedPublicPEM == nil {
		t.Fatal("privileged install did not receive a public certificate")
	}
	block, trailing := pem.Decode(runner.installedPublicPEM)
	if block == nil || block.Type != pemCertificateType || len(trailing) != 0 {
		t.Fatal("privileged install received private or malformed PEM data")
	}

	if err := store.Uninstall(t.Context(), authority); err != nil {
		t.Fatalf("uninstall authority: %v", err)
	}
	if err := store.Uninstall(t.Context(), authority); err != nil {
		t.Fatalf("repeat authority uninstall: %v", err)
	}
	if runner.uninstallCalls != 1 {
		t.Fatalf("uninstall calls = %d, want 1", runner.uninstallCalls)
	}
	if runner.uninstallFingerprint != authority.FingerprintSHA256() {
		t.Fatalf("uninstall fingerprint = %q, want %q", runner.uninstallFingerprint, authority.FingerprintSHA256())
	}
}

func TestDarwinTrustStore_PropagatesAuthorizationFailure(t *testing.T) {
	authority, err := LoadOrCreateAuthority(filepath.Join(t.TempDir(), authorityFileName))
	if err != nil {
		t.Fatalf("create authority: %v", err)
	}
	runner := &fakeDarwinTrustRunner{
		authority:  authority,
		commandErr: errors.New("authorization canceled"),
	}
	store := &darwinTrustStore{runner: runner}
	if err := store.Install(
		t.Context(),
		authority,
	); err == nil ||
		!strings.Contains(err.Error(), "authorization canceled") {
		t.Fatalf("Install() error = %v, want authorization failure", err)
	}
	status, err := store.Status(t.Context(), authority)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.Installed {
		t.Fatal("partially added certificate reported as trusted")
	}
}

type fakeDarwinTrustRunner struct {
	authority            *Authority
	present              bool
	trusted              bool
	commandErr           error
	installCalls         int
	uninstallCalls       int
	installedPublicPEM   []byte
	uninstallFingerprint string
}

func (r *fakeDarwinTrustRunner) CombinedOutput(
	ctx context.Context,
	name string,
	arguments ...string,
) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if name == "/usr/bin/security" {
		switch arguments[0] {
		case "find-certificate":
			want := []string{"find-certificate", "-a", "-p", "-c", AuthorityCommonName, systemKeychainPath}
			if !slices.Equal(arguments, want) {
				return nil, errors.New("unexpected security trust query")
			}
			if !r.present {
				return nil, nil
			}
			return r.authority.PublicCertificatePEM(), nil
		case "verify-cert":
			if !r.trusted {
				return []byte("certificate verification failed"), errors.New("not trusted")
			}
			return nil, nil
		}
	}
	if name != "/usr/bin/security" || len(arguments) < 2 {
		return nil, errors.New("unexpected command")
	}
	if r.commandErr != nil {
		r.present = true
		r.trusted = false
		return []byte("user canceled administrator authorization"), r.commandErr
	}
	switch arguments[0] {
	case "add-trusted-cert":
		r.installCalls++
		certificatePath := arguments[len(arguments)-1]
		var err error
		r.installedPublicPEM, err = os.ReadFile(certificatePath)
		if err != nil {
			return nil, err
		}
		r.present = true
		r.trusted = true
	case "delete-certificate":
		r.uninstallCalls++
		for index, argument := range arguments {
			if argument == "-Z" && index+1 < len(arguments) {
				r.uninstallFingerprint = arguments[index+1]
			}
		}
		r.present = false
		r.trusted = false
	default:
		return nil, errors.New("unexpected security operation")
	}
	return nil, nil
}
