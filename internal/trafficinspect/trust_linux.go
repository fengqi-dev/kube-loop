//go:build linux

package trafficinspect

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kballard/go-shellquote"
)

type linuxTrustBackend struct {
	anchorPath    string
	updateCommand []string
}

type linuxTrustStore struct {
	runner  commandRunner
	backend linuxTrustBackend
	elevate string
	isRoot  func() bool
}

func newSystemTrustStore() TrustStore {
	return &linuxTrustStore{
		runner:  execCommandRunner{},
		backend: detectLinuxTrustBackend(linuxLookPath),
		elevate: detectLinuxElevator(linuxLookPath),
		isRoot:  func() bool { return os.Geteuid() == 0 },
	}
}

func linuxLookPath(name string) (string, error) {
	if path, err := exec.LookPath(name); err == nil {
		return path, nil
	}
	for _, directory := range []string{"/usr/sbin", "/usr/bin", "/sbin", "/bin"} {
		path := filepath.Join(directory, name)
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return path, nil
		}
	}
	return "", fmt.Errorf("%s executable not found", name)
}

func detectLinuxTrustBackend(lookPath func(string) (string, error)) linuxTrustBackend {
	if command, err := lookPath("update-ca-certificates"); err == nil {
		return linuxTrustBackend{
			anchorPath:    "/usr/local/share/ca-certificates/kubeloop-traffic-inspection.crt",
			updateCommand: []string{command},
		}
	}
	if command, err := lookPath("update-ca-trust"); err == nil {
		return linuxTrustBackend{
			anchorPath:    "/etc/pki/ca-trust/source/anchors/kubeloop-traffic-inspection.pem",
			updateCommand: []string{command, "extract"},
		}
	}
	return linuxTrustBackend{}
}

func detectLinuxElevator(lookPath func(string) (string, error)) string {
	if os.Geteuid() == 0 {
		return ""
	}
	if command, err := lookPath("pkexec"); err == nil {
		return command
	}
	if command, err := lookPath("sudo"); err == nil {
		return command
	}
	return ""
}

func (s *linuxTrustStore) Status(_ context.Context, authority *Authority) (TrustStatus, error) {
	if authority == nil {
		return TrustStatus{}, errors.New("traffic inspection authority is required")
	}
	if s.backend.anchorPath == "" {
		return TrustStatus{}, ErrSystemTrustUnsupported
	}
	bundle, err := os.ReadFile(s.backend.anchorPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return TrustStatus{FingerprintSHA256: authority.FingerprintSHA256(), Store: s.backend.anchorPath}, nil
		}
		return TrustStatus{}, fmt.Errorf("read Linux system trust anchor: %w", err)
	}
	return TrustStatus{
		Installed:         containsCertificate(bundle, authority.certificate.Leaf.Raw),
		FingerprintSHA256: authority.FingerprintSHA256(),
		Store:             s.backend.anchorPath,
	}, nil
}

func (s *linuxTrustStore) Install(ctx context.Context, authority *Authority) (returnErr error) {
	status, err := s.Status(ctx, authority)
	if err != nil {
		return err
	}
	if status.Installed {
		return nil
	}
	if !s.runningAsRoot() && s.elevate == "" {
		return errors.New("install Linux traffic inspection certificate: pkexec or sudo is required")
	}
	certificatePath, cleanup, err := writePublicCertificate(authority)
	if err != nil {
		return err
	}
	defer func() {
		if cleanupErr := cleanup(); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
			returnErr = errors.Join(returnErr, fmt.Errorf("remove temporary public certificate: %w", cleanupErr))
		}
	}()
	script := `set -eu
install -d -m 0755 -- "$1"
install -m 0644 -- "$2" "$3"
` + shellquote.Join(s.backend.updateCommand...) + "\n"
	output, err := s.runPrivileged(
		ctx,
		script,
		filepath.Dir(s.backend.anchorPath),
		certificatePath,
		s.backend.anchorPath,
	)
	if err != nil {
		return commandError("install Linux traffic inspection certificate", err, output)
	}
	status, err = s.Status(ctx, authority)
	if err != nil {
		return err
	}
	if !status.Installed {
		return errors.New("Linux traffic inspection certificate was not installed")
	}
	return nil
}

func (s *linuxTrustStore) Uninstall(ctx context.Context, authority *Authority) error {
	status, err := s.Status(ctx, authority)
	if err != nil {
		return err
	}
	if !status.Installed {
		return nil
	}
	if !s.runningAsRoot() && s.elevate == "" {
		return errors.New("uninstall Linux traffic inspection certificate: pkexec or sudo is required")
	}
	script := "set -eu\nrm -f -- \"$1\"\n" + shellquote.Join(s.backend.updateCommand...) + "\n"
	output, err := s.runPrivileged(ctx, script, s.backend.anchorPath)
	if err != nil {
		return commandError("uninstall Linux traffic inspection certificate", err, output)
	}
	status, err = s.Status(ctx, authority)
	if err != nil {
		return err
	}
	if status.Installed {
		return errors.New("Linux traffic inspection certificate is still installed")
	}
	return nil
}

func (s *linuxTrustStore) runningAsRoot() bool {
	if s.isRoot != nil {
		return s.isRoot()
	}
	return os.Geteuid() == 0
}

func (s *linuxTrustStore) runPrivileged(
	ctx context.Context,
	script string,
	arguments ...string,
) ([]byte, error) {
	command := "/bin/sh"
	commandArguments := []string{"-c", script, "kubeloop-trust"}
	commandArguments = append(commandArguments, arguments...)
	if strings.TrimSpace(s.elevate) != "" {
		commandArguments = append([]string{command}, commandArguments...)
		command = s.elevate
	}
	return s.runner.CombinedOutput(ctx, command, commandArguments...)
}
