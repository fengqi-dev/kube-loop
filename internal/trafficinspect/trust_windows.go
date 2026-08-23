//go:build windows

package trafficinspect

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
)

const windowsRootStore = `Cert:\LocalMachine\Root`

const windowsFindCertificateScript = `param([string]$Fingerprint)
$sha = [System.Security.Cryptography.SHA256]::Create()
foreach ($certificate in Get-ChildItem -Path Cert:\LocalMachine\Root) {
  $hash = ([System.BitConverter]::ToString($sha.ComputeHash($certificate.RawData))).Replace('-', '')
  if ($hash -eq $Fingerprint) { Write-Output $certificate.Thumbprint; exit 0 }
}
exit 3`

const windowsInstallCertificateScript = `param([string]$CertificatePath)
$quotedPath = '"' + $CertificatePath + '"'
$process = Start-Process -FilePath certutil.exe -Verb RunAs -Wait -PassThru -ArgumentList @('-addstore', '-f', 'Root', $quotedPath)
exit $process.ExitCode`

const windowsUninstallCertificateScript = `param([string]$Thumbprint)
$process = Start-Process -FilePath certutil.exe -Verb RunAs -Wait -PassThru -ArgumentList @('-delstore', 'Root', $Thumbprint)
exit $process.ExitCode`

type windowsTrustStore struct {
	runner commandRunner
}

func newSystemTrustStore() TrustStore {
	return &windowsTrustStore{runner: execCommandRunner{}}
}

func (s *windowsTrustStore) Status(ctx context.Context, authority *Authority) (TrustStatus, error) {
	if authority == nil {
		return TrustStatus{}, errors.New("traffic inspection authority is required")
	}
	_, installed, err := s.find(ctx, authority)
	if err != nil {
		return TrustStatus{}, err
	}
	return TrustStatus{
		Installed:         installed,
		FingerprintSHA256: authority.FingerprintSHA256(),
		Store:             windowsRootStore,
	}, nil
}

func (s *windowsTrustStore) Install(ctx context.Context, authority *Authority) (returnErr error) {
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
	output, err := s.runner.CombinedOutput(
		ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		windowsInstallCertificateScript, "-CertificatePath", certificatePath,
	)
	if err != nil {
		return commandError("install Windows traffic inspection certificate", err, output)
	}
	status, err = s.Status(ctx, authority)
	if err != nil {
		return err
	}
	if !status.Installed {
		return errors.New("windows traffic inspection certificate was not installed")
	}
	return nil
}

func (s *windowsTrustStore) Uninstall(ctx context.Context, authority *Authority) error {
	thumbprint, installed, err := s.find(ctx, authority)
	if err != nil {
		return err
	}
	if !installed {
		return nil
	}
	output, err := s.runner.CombinedOutput(
		ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		windowsUninstallCertificateScript, "-Thumbprint", thumbprint,
	)
	if err != nil {
		return commandError("uninstall Windows traffic inspection certificate", err, output)
	}
	_, installed, err = s.find(ctx, authority)
	if err != nil {
		return err
	}
	if installed {
		return errors.New("windows traffic inspection certificate is still installed")
	}
	return nil
}

func (s *windowsTrustStore) find(ctx context.Context, authority *Authority) (string, bool, error) {
	output, err := s.runner.CombinedOutput(
		ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		windowsFindCertificateScript, "-Fingerprint", authority.FingerprintSHA256(),
	)
	if err != nil {
		if exitCodeIs(err, 3) {
			return "", false, nil
		}
		return "", false, commandError("inspect Windows system certificate trust", err, output)
	}
	thumbprint := strings.TrimSpace(string(output))
	if thumbprint == "" {
		return "", false, nil
	}
	return thumbprint, true, nil
}
