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
			return helperinstall.UninstallWithCertificate(ctx, "")
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
	fingerprint := ""
	if status.Installed {
		fingerprint = authority.FingerprintSHA256()
	}
	if err := helperinstall.UninstallWithCertificate(ctx, fingerprint); err != nil {
		return err
	}
	status, err = store.Status(ctx, authority)
	if err != nil {
		return err
	}
	if status.Installed {
		return errors.New("macOS traffic inspection certificate is still installed")
	}
	return nil
}
