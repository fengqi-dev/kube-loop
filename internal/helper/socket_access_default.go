//go:build !windows

package helper

import "os"

func configureHelperSocketAccess(path, _ string) error {
	// The helper authenticates every request with a high-entropy token; the
	// socket must remain reachable by the unprivileged desktop user.
	//nolint:gosec // World socket access is intentional and does not bypass request authentication.
	return os.Chmod(path, 0o666)
}
