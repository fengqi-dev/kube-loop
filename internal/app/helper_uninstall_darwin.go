//go:build darwin

package app

import (
	"context"
	"errors"
	"fmt"
	"os"

	helperinstall "github.com/fengqi-dev/kube-loop/internal/helper/install"
	"github.com/fengqi-dev/kube-loop/internal/trafficinspect"
)

func (a *App) uninstallHelperAndTrust(ctx context.Context) error {
	path, err := a.trafficInspectionAuthorityPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return helperinstall.UninstallWithCertificate(ctx, nil)
		}
		return fmt.Errorf("inspect traffic inspection authority: %w", err)
	}
	authority, err := trafficinspect.LoadOrCreateAuthority(path)
	if err != nil {
		return fmt.Errorf("load traffic inspection authority for removal: %w", err)
	}
	return helperinstall.UninstallWithCertificate(ctx, authority.PublicCertificatePEM())
}
