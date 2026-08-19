//go:build windows

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
	return elevateWindowsInstall(
		ctx, source, sourceSHA256, token, uid, home, singBox, certificatePEM,
	)
}
