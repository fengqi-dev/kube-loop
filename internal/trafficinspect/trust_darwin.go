//go:build darwin

package trafficinspect

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"

	"github.com/kballard/go-shellquote"
)

const systemKeychainPath = "/Library/Keychains/System.keychain"

func exitCodeIs(err error, code int) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ExitCode() == code
}

type darwinTrustStore struct {
	runner commandRunner
}

func newSystemTrustStore() TrustStore {
	return &darwinTrustStore{runner: execCommandRunner{}}
}

func (s *darwinTrustStore) Status(ctx context.Context, authority *Authority) (TrustStatus, error) {
	if authority == nil {
		return TrustStatus{}, errors.New("traffic inspection authority is required")
	}
	output, err := s.runner.CombinedOutput(
		ctx,
		"/usr/bin/security",
		"find-certificate", "-a", "-p", "-c", AuthorityCommonName, systemKeychainPath,
	)
	if err != nil {
		if exitCodeIs(err, 44) {
			return TrustStatus{FingerprintSHA256: authority.FingerprintSHA256(), Store: systemKeychainPath}, nil
		}
		return TrustStatus{}, commandError("inspect macOS system certificate trust", err, output)
	}
	installed := containsCertificate(output, authority.certificate.Leaf.Raw)
	return TrustStatus{
		Installed:         installed,
		FingerprintSHA256: authority.FingerprintSHA256(),
		Store:             systemKeychainPath,
	}, nil
}

func (s *darwinTrustStore) Install(ctx context.Context, authority *Authority) (returnErr error) {
	status, err := s.Status(ctx, authority)
	if err != nil {
		return err
	}
	if status.Installed {
		return nil
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

	command := shellquote.Join(
		"/usr/bin/security", "add-trusted-cert", "-d", "-r", "trustRoot",
		"-k", systemKeychainPath, certificatePath,
	)
	script := "do shell script " + strconv.Quote(command) + " with administrator privileges"
	output, err := s.runner.CombinedOutput(ctx, "/usr/bin/osascript", "-e", script)
	if err != nil {
		return commandError("install macOS traffic inspection certificate", err, output)
	}
	status, err = s.Status(ctx, authority)
	if err != nil {
		return err
	}
	if !status.Installed {
		return errors.New("macOS traffic inspection certificate was not installed")
	}
	return nil
}

func (s *darwinTrustStore) Uninstall(ctx context.Context, authority *Authority) error {
	status, err := s.Status(ctx, authority)
	if err != nil {
		return err
	}
	if !status.Installed {
		return nil
	}
	command := shellquote.Join(
		"/usr/bin/security", "delete-certificate", "-Z", authority.FingerprintSHA256(),
		"-t", systemKeychainPath,
	)
	script := "do shell script " + strconv.Quote(command) + " with administrator privileges"
	output, err := s.runner.CombinedOutput(ctx, "/usr/bin/osascript", "-e", script)
	if err != nil {
		return commandError("uninstall macOS traffic inspection certificate", err, output)
	}
	status, err = s.Status(ctx, authority)
	if err != nil {
		return err
	}
	if status.Installed {
		return errors.New("macOS traffic inspection certificate is still installed")
	}
	return nil
}
