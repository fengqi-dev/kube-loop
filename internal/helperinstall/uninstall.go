package helperinstall

import "context"

// Uninstall removes the helper service (requires elevation).
func Uninstall(ctx context.Context) error {
	source, err := LocateBundledHelper()
	if err != nil {
		return err
	}
	return ElevateUninstall(ctx, source)
}
