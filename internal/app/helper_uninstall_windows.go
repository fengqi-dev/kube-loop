//go:build windows

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
			return helperinstall.UninstallWindowsWithCertificate(ctx, nil)
		}
		return fmt.Errorf("inspect traffic inspection authority: %w", err)
	}
	authority, err := trafficinspect.LoadOrCreateAuthority(path)
	if err != nil {
		return fmt.Errorf("load traffic inspection authority for removal: %w", err)
	}
	store := a.trafficInspectionTrust
	if store == nil {
		store = trafficinspect.NewSystemTrustStore()
	}
	status, err := store.Status(ctx, authority)
	if err != nil {
		return err
	}
	var certificatePEM []byte
	if status.Installed {
		certificatePEM = authority.PublicCertificatePEM()
	}
	if err := helperinstall.UninstallWindowsWithCertificate(ctx, certificatePEM); err != nil {
		return err
	}
	status, err = store.Status(ctx, authority)
	if err != nil {
		return err
	}
	if status.Installed {
		return errors.New("windows traffic inspection certificate is still installed")
	}
	return nil
}
