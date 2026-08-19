//go:build linux

package install

import (
	"bytes"
	"context"
	"encoding/pem"
	"fmt"
	"os"
)

func requiresSupervisorCheck(bool) bool { return false }

func installCurrentHelper(
	ctx context.Context,
	source, sourceSHA256, token string,
	uid int,
	home, singBox string,
	certificatePEM []byte,
) error {
	certificatePath, cleanup, err := writeTemporaryLinuxCertificate(certificatePEM)
	if err != nil {
		return err
	}
	defer cleanup()
	return elevateLinuxInstall(
		ctx, source, sourceSHA256, token, uid, home, singBox, certificatePath,
	)
}

func writeTemporaryLinuxCertificate(content []byte) (string, func(), error) {
	if len(content) == 0 {
		return "", func() {}, nil
	}
	block, trailing := pem.Decode(content)
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(trailing)) != 0 {
		return "", func() {}, fmt.Errorf("traffic inspection certificate PEM is invalid")
	}
	file, err := os.CreateTemp("", "kubeloop-inspection-ca-*.pem")
	if err != nil {
		return "", func() {}, fmt.Errorf("create temporary traffic inspection certificate: %w", err)
	}
	path := file.Name()
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(path)
	}
	if err := file.Chmod(0o600); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("secure temporary traffic inspection certificate: %w", err)
	}
	if _, err := file.Write(content); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("write temporary traffic inspection certificate: %w", err)
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("close temporary traffic inspection certificate: %w", err)
	}
	return path, cleanup, nil
}
