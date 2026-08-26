//go:build darwin

package trafficinspect

import (
	"context"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/kballard/go-shellquote"
)

func TestDarwinTrustStore_InstallAndUninstallAreIdempotent(t *testing.T) {
	authority, err := LoadOrCreateAuthority(filepath.Join(t.TempDir(), authorityFileName))
	if err != nil {
		t.Fatalf("create authority: %v", err)
	}
	runner := &fakeDarwinTrustRunner{authority: authority}
	store := &darwinTrustStore{runner: runner, settings: runner}

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
	if runner.trustInstallCalls != 1 {
		t.Fatalf("trust install calls = %d, want 1", runner.trustInstallCalls)
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
	if runner.trustUninstallCalls != 1 {
		t.Fatalf("trust uninstall calls = %d, want 1", runner.trustUninstallCalls)
	}
	wantKeychainFingerprint, err := darwinKeychainFingerprint(authority)
	if err != nil {
		t.Fatal(err)
	}
	if runner.uninstallFingerprint != wantKeychainFingerprint {
		t.Fatalf("uninstall fingerprint = %q, want %q", runner.uninstallFingerprint, wantKeychainFingerprint)
	}
	if runner.uninstallFingerprint == authority.FingerprintSHA256() {
		t.Fatal("uninstall used SHA-256 instead of the macOS Keychain SHA-1 identifier")
	}
}

func TestDarwinTrustStore_RetriesTrustAfterAuthorizationFailure(t *testing.T) {
	authority, err := LoadOrCreateAuthority(filepath.Join(t.TempDir(), authorityFileName))
	if err != nil {
		t.Fatalf("create authority: %v", err)
	}
	runner := &fakeDarwinTrustRunner{
		authority: authority,
		nativeErr: errors.New("authorization canceled"),
	}
	store := &darwinTrustStore{runner: runner, settings: runner}
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
	if !runner.present {
		t.Fatal("certificate was not retained for a trust-only retry")
	}
	runner.nativeErr = nil
	if err := store.Install(t.Context(), authority); err != nil {
		t.Fatalf("retry install authority: %v", err)
	}
	if runner.installCalls != 1 {
		t.Fatalf("keychain install calls = %d, want 1", runner.installCalls)
	}
	if runner.trustInstallCalls != 2 {
		t.Fatalf("trust install calls = %d, want 2", runner.trustInstallCalls)
	}
}

func TestDarwinTrustStore_UninstallRemovesDuplicateCertificateEntries(t *testing.T) {
	authority, err := LoadOrCreateAuthority(filepath.Join(t.TempDir(), authorityFileName))
	if err != nil {
		t.Fatalf("create authority: %v", err)
	}
	runner := &fakeDarwinTrustRunner{
		authority:          authority,
		present:            true,
		deleteKeepsPresent: 1,
	}
	store := &darwinTrustStore{runner: runner, settings: runner}
	if err := store.Uninstall(t.Context(), authority); err != nil {
		t.Fatalf("uninstall duplicate authority: %v", err)
	}
	if runner.uninstallCalls != 2 {
		t.Fatalf("uninstall calls = %d, want 2", runner.uninstallCalls)
	}
}

func TestDarwinTrustStore_PrivilegedLifecycleUsesNoAdministratorScript(t *testing.T) {
	authority, err := LoadOrCreateAuthority(filepath.Join(t.TempDir(), authorityFileName))
	if err != nil {
		t.Fatalf("create authority: %v", err)
	}
	runner := &fakeDarwinTrustRunner{authority: authority}
	store := &darwinTrustStore{runner: runner, settings: runner}
	if err := store.install(t.Context(), authority, true); err != nil {
		t.Fatalf("privileged install: %v", err)
	}
	if err := store.uninstall(t.Context(), authority, true); err != nil {
		t.Fatalf("privileged uninstall: %v", err)
	}
	if runner.administratorScripts != 0 {
		t.Fatalf("administrator scripts = %d, want 0", runner.administratorScripts)
	}
	if runner.installCalls != 1 || runner.trustInstallCalls != 1 ||
		runner.uninstallCalls != 1 || runner.trustUninstallCalls != 1 {
		t.Fatalf("privileged lifecycle calls = %#v", runner)
	}
}

type fakeDarwinTrustRunner struct {
	authority            *Authority
	present              bool
	trusted              bool
	commandErr           error
	nativeErr            error
	installCalls         int
	uninstallCalls       int
	trustInstallCalls    int
	trustUninstallCalls  int
	deleteKeepsPresent   int
	installedPublicPEM   []byte
	uninstallFingerprint string
	administratorScripts int
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
		want := []string{"find-certificate", "-a", "-p", "-c", AuthorityCommonName, systemKeychainPath}
		if slices.Equal(arguments, want) {
			if !r.present {
				return nil, nil
			}
			return r.authority.PublicCertificatePEM(), nil
		}
		return r.runSecurityCommand(arguments)
	}
	if name != "/usr/bin/osascript" || len(arguments) != 2 || arguments[0] != "-e" {
		return nil, errors.New("unexpected command")
	}
	if r.commandErr != nil {
		return []byte("user canceled administrator authorization"), r.commandErr
	}
	r.administratorScripts++
	command, err := decodeAdministratorScript(arguments[1])
	if err != nil {
		return nil, err
	}
	commandArguments, err := shellquote.Split(command)
	if err != nil {
		return nil, err
	}
	if len(commandArguments) < 2 || commandArguments[0] != "/usr/bin/security" {
		return nil, errors.New("unexpected privileged command")
	}
	return r.runSecurityCommand(commandArguments[1:])
}

func (r *fakeDarwinTrustRunner) runSecurityCommand(arguments []string) ([]byte, error) {
	if len(arguments) == 0 {
		return nil, errors.New("missing security operation")
	}
	switch arguments[0] {
	case "add-certificates":
		r.installCalls++
		certificatePath := arguments[len(arguments)-1]
		content, err := os.ReadFile(certificatePath)
		if err != nil {
			return nil, err
		}
		r.installedPublicPEM = content
		r.present = true
	case "delete-certificate":
		if slices.Contains(arguments, "-t") {
			return nil, errors.New("certificate removal must not repeat trust-settings deletion")
		}
		r.uninstallCalls++
		for index, argument := range arguments {
			if argument == "-Z" && index+1 < len(arguments) {
				r.uninstallFingerprint = arguments[index+1]
			}
		}
		if r.deleteKeepsPresent > 0 {
			r.deleteKeepsPresent--
		} else {
			r.present = false
		}
		r.trusted = false
	default:
		return nil, errors.New("unexpected security operation")
	}
	return nil, nil
}

func (r *fakeDarwinTrustRunner) Installed(*Authority) (bool, error) {
	return r.trusted, nil
}

func (r *fakeDarwinTrustRunner) Install(*Authority) error {
	r.trustInstallCalls++
	if r.nativeErr != nil {
		return r.nativeErr
	}
	r.trusted = true
	return nil
}

func (r *fakeDarwinTrustRunner) Uninstall(*Authority) error {
	r.trustUninstallCalls++
	if r.nativeErr != nil {
		return r.nativeErr
	}
	r.trusted = false
	return nil
}

func decodeAdministratorScript(script string) (string, error) {
	const prefix = "do shell script "
	const suffix = " with administrator privileges"
	if !strings.HasPrefix(script, prefix) || !strings.HasSuffix(script, suffix) {
		return "", errors.New("invalid administrator script")
	}
	quoted := strings.TrimSuffix(strings.TrimPrefix(script, prefix), suffix)
	command, err := strconv.Unquote(quoted)
	if err != nil {
		return "", err
	}
	return command, nil
}
