//go:build linux

package install

import "context"

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
	return writeTemporaryPublicCertificate(content)
}
