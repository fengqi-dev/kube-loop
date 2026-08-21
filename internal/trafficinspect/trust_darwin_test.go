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
		t.Fatalf("privileged install calls = %d, want 1", runner.installCalls)
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
		t.Fatalf("privileged uninstall calls = %d, want 1", runner.uninstallCalls)
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
		authority:     authority,
		privilegedErr: errors.New("authorization canceled"),
	}
	store := &darwinTrustStore{runner: runner}
	if err := store.Install(
		t.Context(),
		authority,
	); err == nil ||
		!strings.Contains(err.Error(), "authorization canceled") {
		t.Fatalf("Install() error = %v, want authorization failure", err)
	}
}

type fakeDarwinTrustRunner struct {
	authority            *Authority
	installed            bool
	privilegedErr        error
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
		want := []string{"find-certificate", "-a", "-p", "-c", AuthorityCommonName, systemKeychainPath}
		if !slices.Equal(arguments, want) {
			return nil, errors.New("unexpected security trust query")
		}
		if !r.installed {
			return nil, nil
		}
		return r.authority.PublicCertificatePEM(), nil
	}
	if name != "/usr/bin/osascript" || len(arguments) != 2 || arguments[0] != "-e" {
		return nil, errors.New("unexpected command")
	}
	if r.privilegedErr != nil {
		return []byte("user canceled administrator authorization"), r.privilegedErr
	}
	command, err := decodeAdministratorScript(arguments[1])
	if err != nil {
		return nil, err
	}
	commandArguments, err := shellquote.Split(command)
	if err != nil {
		return nil, err
	}
	if len(commandArguments) < 2 {
		return nil, errors.New("privileged command has too few arguments")
	}
	if commandArguments[0] != "/usr/bin/security" {
		return nil, errors.New("unexpected privileged command")
	}
	switch commandArguments[1] {
	case "add-trusted-cert":
		r.installCalls++
		certificatePath := commandArguments[len(commandArguments)-1]
		r.installedPublicPEM, err = os.ReadFile(certificatePath)
		if err != nil {
			return nil, err
		}
		r.installed = true
	case "delete-certificate":
		r.uninstallCalls++
		for index, argument := range commandArguments {
			if argument == "-Z" && index+1 < len(commandArguments) {
				r.uninstallFingerprint = commandArguments[index+1]
			}
		}
		r.installed = false
	default:
		return nil, errors.New("unexpected privileged security operation")
	}
	return nil, nil
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
