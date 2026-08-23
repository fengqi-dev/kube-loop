//go:build windows

package install

import (
	"bytes"
	"crypto/sha1" // #nosec G505 -- Windows certificate-store thumbprint identifier, not a security digest.
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func RunElevatedRequest(operation, requestPath, resultPath string) error {
	err := executeElevatedRequest(operation, requestPath)
	result := elevatedResult{}
	if err != nil {
		result.Error = err.Error()
	}
	raw, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		return marshalErr
	}
	if writeErr := os.WriteFile(resultPath, raw, 0o600); writeErr != nil {
		return fmt.Errorf("write elevated result: %w", writeErr)
	}
	return err
}

func executeElevatedRequest(operation, requestPath string) error {
	raw, err := os.ReadFile(requestPath)
	if err != nil {
		return fmt.Errorf("read elevated request: %w", err)
	}
	var request elevatedRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return fmt.Errorf("decode elevated request: %w", err)
	}
	if request.ExpectedSHA256 == "" {
		return fmt.Errorf("expected helper SHA-256 is required")
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find elevated helper executable: %w", err)
	}
	actual, err := fileSHA256(executable)
	if err != nil {
		return fmt.Errorf("hash elevated helper executable: %w", err)
	}
	if !strings.EqualFold(actual, request.ExpectedSHA256) {
		return fmt.Errorf("elevated helper checksum mismatch")
	}
	switch operation {
	case "install":
		return elevatedInstall(request)
	case "uninstall":
		certificateErr := uninstallWindowsCertificate(request.CertificatePEM, runWindowsCommand)
		return errors.Join(certificateErr, UninstallFromCLI())
	default:
		return fmt.Errorf("unsupported elevated operation %q", operation)
	}
}

func elevatedInstall(request elevatedRequest) error {
	source := request.ServiceSource
	if source == "" {
		executable, err := os.Executable()
		if err != nil {
			return fmt.Errorf("find elevated install tool: %w", err)
		}
		executable, err = filepath.EvalSymlinks(executable)
		if err != nil {
			return fmt.Errorf("resolve elevated install tool: %w", err)
		}
		source = filepath.Join(filepath.Dir(executable), "kubeloop-helper.exe")
	}
	source, err := filepath.EvalSymlinks(source)
	if err != nil {
		return fmt.Errorf("resolve helper service source: %w", err)
	}
	if request.ServiceSHA256 != "" {
		actual, hashErr := fileSHA256(source)
		if hashErr != nil {
			return fmt.Errorf("hash helper service source: %w", hashErr)
		}
		if !strings.EqualFold(actual, request.ServiceSHA256) {
			return fmt.Errorf("bundled helper checksum mismatch")
		}
	}
	if err := InstallFromCLI(
		source,
		request.Token,
		request.UID,
		request.Version,
		request.HomeDir,
		request.OwnerSID,
		request.SingBoxPath,
	); err != nil {
		return err
	}
	return installWindowsCertificate(request.CertificatePEM, runWindowsCommand)
}

type windowsCommandRunner func(string, ...string) ([]byte, error)

func runWindowsCommand(name string, arguments ...string) ([]byte, error) {
	return exec.Command(name, arguments...).CombinedOutput()
}

func installWindowsCertificate(content []byte, run windowsCommandRunner) (returnErr error) {
	if len(content) == 0 {
		return nil
	}
	if _, err := parseWindowsCertificate(content); err != nil {
		return err
	}
	file, err := os.CreateTemp("", "kubeloop-inspection-ca-*.pem")
	if err != nil {
		return fmt.Errorf("create temporary traffic inspection certificate: %w", err)
	}
	path := file.Name()
	defer func() {
		if cleanupErr := os.Remove(path); cleanupErr != nil && !os.IsNotExist(cleanupErr) {
			returnErr = errors.Join(
				returnErr,
				fmt.Errorf("remove temporary traffic inspection certificate: %w", cleanupErr),
			)
		}
	}()
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return fmt.Errorf("write temporary traffic inspection certificate: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close temporary traffic inspection certificate: %w", err)
	}
	if run == nil {
		return fmt.Errorf("windows certificate command runner is required")
	}
	output, err := run("certutil.exe", "-addstore", "-f", "Root", path)
	if err != nil {
		return fmt.Errorf(
			"install Windows traffic inspection certificate: %w: %s",
			err,
			strings.TrimSpace(string(output)),
		)
	}
	return nil
}

func uninstallWindowsCertificate(content []byte, run windowsCommandRunner) error {
	if len(content) == 0 {
		return nil
	}
	certificate, err := parseWindowsCertificate(content)
	if err != nil {
		return err
	}
	if run == nil {
		return fmt.Errorf("windows certificate command runner is required")
	}
	identifier := sha1.Sum(certificate.Raw) // #nosec G401 -- certutil identifies store entries by SHA-1 thumbprint.
	thumbprint := strings.ToUpper(hex.EncodeToString(identifier[:]))
	output, err := run("certutil.exe", "-delstore", "Root", thumbprint)
	if err != nil {
		return fmt.Errorf(
			"uninstall Windows traffic inspection certificate: %w: %s",
			err,
			strings.TrimSpace(string(output)),
		)
	}
	return nil
}

func parseWindowsCertificate(content []byte) (*x509.Certificate, error) {
	block, trailing := pem.Decode(content)
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(trailing)) != 0 {
		return nil, fmt.Errorf("traffic inspection certificate PEM is invalid")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse traffic inspection certificate: %w", err)
	}
	return certificate, nil
}
