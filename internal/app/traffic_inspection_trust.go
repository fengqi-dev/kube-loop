package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/trafficinspect"
)

func (a *App) installTrafficInspectionTrust(ctx context.Context) error {
	if a == nil || a.trafficInspectionEnabled == nil || !a.trafficInspectionEnabled.Load() {
		return nil
	}
	return a.ensureTrafficInspectionTrust(ctx)
}

func (a *App) pendingTrafficInspectionCertificate(ctx context.Context) ([]byte, error) {
	if a == nil || a.trafficInspectionEnabled == nil || !a.trafficInspectionEnabled.Load() {
		return nil, nil
	}
	path, err := a.trafficInspectionAuthorityPath()
	if err != nil {
		return nil, err
	}
	authority, err := trafficinspect.LoadOrCreateAuthority(path)
	if err != nil {
		return nil, fmt.Errorf("load traffic inspection authority: %w", err)
	}
	store := a.trafficInspectionTrust
	if store == nil {
		store = trafficinspect.NewSystemTrustStore()
	}
	status, err := store.Status(ctx, authority)
	if err != nil {
		return nil, fmt.Errorf("inspect system certificate trust: %w", err)
	}
	if status.Installed {
		return nil, nil
	}
	return authority.PublicCertificatePEM(), nil
}

func (a *App) ensureTrafficInspectionTrust(ctx context.Context) error {
	path, err := a.trafficInspectionAuthorityPath()
	if err != nil {
		return err
	}
	authority, err := trafficinspect.LoadOrCreateAuthority(path)
	if err != nil {
		return fmt.Errorf("load traffic inspection authority: %w", err)
	}
	store := a.trafficInspectionTrust
	if store == nil {
		store = trafficinspect.NewSystemTrustStore()
	}
	if err := store.Install(ctx, authority); err != nil {
		return fmt.Errorf("install system certificate trust: %w", err)
	}
	return nil
}

func (a *App) uninstallTrafficInspectionTrust(ctx context.Context) error {
	if a == nil {
		return nil
	}
	path, err := a.trafficInspectionAuthorityPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
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
	if err := store.Uninstall(ctx, authority); err != nil {
		return fmt.Errorf("uninstall system certificate trust: %w", err)
	}
	return nil
}

func (a *App) trafficInspectionAuthorityPath() (string, error) {
	if path := strings.TrimSpace(a.trafficInspectionCAPath); path != "" {
		return path, nil
	}
	path, err := trafficinspect.DefaultAuthorityPath()
	if err != nil {
		return "", fmt.Errorf("resolve traffic inspection authority path: %w", err)
	}
	return path, nil
}
