package trafficinspect

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type commandRunner interface {
	CombinedOutput(context.Context, string, ...string) ([]byte, error)
}

type execCommandRunner struct{}

func (execCommandRunner) CombinedOutput(ctx context.Context, name string, arguments ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, arguments...).CombinedOutput()
}

func writePublicCertificate(authority *Authority) (string, func() error, error) {
	if authority == nil {
		return "", func() error { return nil }, errors.New("traffic inspection authority is required")
	}
	file, err := os.CreateTemp("", "kubeloop-inspection-ca-*.pem")
	if err != nil {
		return "", func() error { return nil }, fmt.Errorf("create temporary public certificate: %w", err)
	}
	path := file.Name()
	cleanup := func() error { return os.Remove(path) }
	if err := file.Chmod(0o600); err != nil {
		return "", func() error { return nil }, errors.Join(
			fmt.Errorf("set temporary public certificate permissions: %w", err),
			file.Close(), cleanup(),
		)
	}
	if _, err := file.Write(authority.PublicCertificatePEM()); err != nil {
		return "", func() error { return nil }, errors.Join(
			fmt.Errorf("write temporary public certificate: %w", err),
			file.Close(), cleanup(),
		)
	}
	if err := file.Close(); err != nil {
		return "", func() error { return nil }, errors.Join(
			fmt.Errorf("close temporary public certificate: %w", err), cleanup(),
		)
	}
	return path, cleanup, nil
}

func containsCertificate(bundle []byte, expected []byte) bool {
	for len(bundle) > 0 {
		block, remainder := pem.Decode(bundle)
		if block == nil {
			return false
		}
		bundle = remainder
		if block.Type != pemCertificateType {
			continue
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err == nil && bytes.Equal(certificate.Raw, expected) {
			return true
		}
	}
	return false
}

func commandError(operation string, err error, output []byte) error {
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return fmt.Errorf("%s: %w: %s", operation, err, detail)
}
