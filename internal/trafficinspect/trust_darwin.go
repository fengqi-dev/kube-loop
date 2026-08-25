//go:build darwin

package trafficinspect

import (
	"context"
	"errors"
	"fmt"
	"os"
)

const systemKeychainPath = "/Library/Keychains/System.keychain"

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
	if installed {
		// Presence in System.keychain is not sufficient: add-trusted-cert can add
		// the certificate before its separate trust-settings authorization fails.
		certificatePath, cleanup, writeErr := writePublicCertificate(authority)
		if writeErr != nil {
			return TrustStatus{}, writeErr
		}
		defer func() { _ = cleanup() }()
		_, verifyErr := s.runner.CombinedOutput(
			ctx,
			"/usr/bin/security",
			"verify-cert", "-q", "-l", "-L", "-c", certificatePath,
			"-k", systemKeychainPath,
		)
		if verifyErr != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return TrustStatus{}, ctxErr
			}
			installed = false
		}
	}
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

	arguments := []string{
		"add-trusted-cert", "-d", "-r", "trustRoot",
		"-k", systemKeychainPath, certificatePath,
	}
	// Run security directly so SecurityAgent can present its own administrator
	// authorization. Nesting this command inside an already elevated osascript
	// prevents that second interaction on current macOS releases.
	output, err := s.runner.CombinedOutput(ctx, "/usr/bin/security", arguments...)
	if err != nil {
		status, statusErr := s.Status(ctx, authority)
		if statusErr == nil && status.Installed {
			return nil
		}
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
	arguments := []string{
		"delete-certificate", "-Z", authority.FingerprintSHA256(),
		"-t", systemKeychainPath,
	}
	output, err := s.runner.CombinedOutput(ctx, "/usr/bin/security", arguments...)
	if err != nil {
		status, statusErr := s.Status(ctx, authority)
		if statusErr == nil && !status.Installed {
			return nil
		}
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
