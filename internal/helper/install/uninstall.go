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
func UninstallWithCertificate(ctx context.Context, fingerprint string) error {
	source, err := LocateBundledHelper()
	if err != nil {
		return err
	}
	return ElevateUninstallWithCertificate(ctx, source, fingerprint)
}
