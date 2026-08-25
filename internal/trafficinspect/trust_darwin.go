//go:build darwin

package trafficinspect

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/kballard/go-shellquote"
)

const systemKeychainPath = "/Library/Keychains/System.keychain"

const maxDarwinCertificateRemovalAttempts = 8

type darwinTrustStore struct {
	runner   commandRunner
	settings darwinTrustSettings
}

type darwinTrustSettings interface {
	Installed(*Authority) (bool, error)
	Install(*Authority) error
	Uninstall(*Authority) error
}

func newSystemTrustStore() TrustStore {
	return &darwinTrustStore{runner: execCommandRunner{}, settings: newDarwinTrustSettings()}
}

func (s *darwinTrustStore) Status(ctx context.Context, authority *Authority) (TrustStatus, error) {
	if authority == nil {
		return TrustStatus{}, errors.New("traffic inspection authority is required")
	}
	present, err := s.certificatePresent(ctx, authority)
	if err != nil {
		return TrustStatus{}, err
	}
	installed := false
	if present {
		installed, err = s.settings.Installed(authority)
		if err != nil {
			return TrustStatus{}, fmt.Errorf("inspect macOS certificate trust settings: %w", err)
		}
	}
	return TrustStatus{
		Installed:         installed,
		FingerprintSHA256: authority.FingerprintSHA256(),
		Store:             systemKeychainPath,
	}, nil
}

func (s *darwinTrustStore) certificatePresent(ctx context.Context, authority *Authority) (bool, error) {
	output, err := s.runner.CombinedOutput(
		ctx,
		"/usr/bin/security",
		"find-certificate", "-a", "-p", "-c", AuthorityCommonName, systemKeychainPath,
	)
	if err != nil {
		if exitCodeIs(err, 44) {
			return false, nil
		}
		return false, commandError("inspect macOS system certificate", err, output)
	}
	return containsCertificate(output, authority.certificate.Leaf.Raw), nil
}

func (s *darwinTrustStore) Install(ctx context.Context, authority *Authority) (returnErr error) {
	status, err := s.Status(ctx, authority)
	if err != nil {
		return err
	}
	if status.Installed {
		return nil
	}
	present, err := s.certificatePresent(ctx, authority)
	if err != nil {
		return err
	}
	if !present {
		certificatePath, cleanup, writeErr := writePublicCertificate(authority)
		if writeErr != nil {
			return writeErr
		}
		defer func() {
			if cleanupErr := cleanup(); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
				returnErr = errors.Join(returnErr, fmt.Errorf("remove temporary public certificate: %w", cleanupErr))
			}
		}()
		command := shellquote.Join(
			"/usr/bin/security", "add-certificates", "-k", systemKeychainPath, certificatePath,
		)
		script := "do shell script " + strconv.Quote(command) + " with administrator privileges"
		output, commandErr := s.runner.CombinedOutput(ctx, "/usr/bin/osascript", "-e", script)
		if commandErr != nil {
			return commandError("add macOS traffic inspection certificate", commandErr, output)
		}
	}
	if err := s.settings.Install(authority); err != nil {
		return fmt.Errorf("authorize macOS traffic inspection certificate trust: %w", err)
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
	if authority == nil {
		return errors.New("traffic inspection authority is required")
	}
	present, err := s.certificatePresent(ctx, authority)
	if err != nil {
		return err
	}
	if !present {
		return nil
	}
	trusted, err := s.settings.Installed(authority)
	if err != nil {
		return fmt.Errorf("inspect macOS certificate trust settings: %w", err)
	}
	if trusted {
		if err := s.settings.Uninstall(authority); err != nil {
			return fmt.Errorf("remove macOS traffic inspection certificate trust: %w", err)
		}
	}
	for range maxDarwinCertificateRemovalAttempts {
		command := shellquote.Join(
			"/usr/bin/security", "delete-certificate", "-Z", authority.FingerprintSHA256(),
			systemKeychainPath,
		)
		script := "do shell script " + strconv.Quote(command) + " with administrator privileges"
		output, commandErr := s.runner.CombinedOutput(ctx, "/usr/bin/osascript", "-e", script)
		if commandErr != nil {
			return commandError("remove macOS traffic inspection certificate", commandErr, output)
		}
		present, err = s.certificatePresent(ctx, authority)
		if err != nil {
			return err
		}
		if !present {
			return nil
		}
	}
	return errors.New("macOS traffic inspection certificate is still installed after repeated removal")
}
