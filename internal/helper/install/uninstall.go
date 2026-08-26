package install

import "context"

// Uninstall removes the helper service (requires elevation).
func Uninstall(ctx context.Context) error {
	source, err := LocateBundledHelper()
	if err != nil {
		return err
	}
	return ElevateUninstall(ctx, source)
}

// UninstallWithCertificate removes the macOS helper and an optional system
// trust certificate in one elevated operation. Other platforms use Uninstall.
func UninstallWithCertificate(ctx context.Context, certificatePEM []byte) error {
	source, err := LocateBundledHelper()
	if err != nil {
		return err
	}
	certificatePath, cleanup, err := writeTemporaryPublicCertificate(certificatePEM)
	if err != nil {
		return err
	}
	defer cleanup()
	return ElevateUninstallWithCertificate(ctx, source, certificatePath)
}
