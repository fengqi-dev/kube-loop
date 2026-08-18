//go:build !darwin

package app

import (
	"context"

	helperinstall "github.com/fengqi-dev/kube-loop/internal/helper/install"
)

func (a *App) uninstallHelperAndTrust(ctx context.Context) error {
	if err := a.uninstallTrafficInspectionTrust(ctx); err != nil {
		return err
	}
	return helperinstall.Uninstall(ctx)
}
