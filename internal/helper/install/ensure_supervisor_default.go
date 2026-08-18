//go:build !darwin

package install

import "context"

func requiresSupervisorCheck(bool) bool { return false }

func installCurrentHelper(
	ctx context.Context,
	source, sourceSHA256, token string,
	uid int,
	home, singBox string,
	_ []byte,
) error {
	return ElevateInstall(ctx, source, sourceSHA256, token, uid, home, singBox)
}
