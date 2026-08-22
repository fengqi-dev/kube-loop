package localuser

import (
	"context"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

func (service *Service) SetEnabled(
	ctx context.Context,
	identityID string,
	enabled bool,
) error {
	now := service.now().UTC()
	return service.store.WithinTransaction(
		ctx,
		func(repositories storage.Repositories) error {
			if err := repositories.Credentials().SetPasswordEnabled(ctx, identityID, enabled, now); err != nil {
				return err
			}
			if !enabled {
				_, err := repositories.OAuthSessions().
					RevokeIdentity(ctx, identityID, now)
				return err
			}
			return nil
		},
	)
}

func (service *Service) SetPassword(
	ctx context.Context,
	identityID string,
	password []byte,
) error {
	if len(password) < minimumPasswordLen ||
		len(password) > maximumPasswordLen {
		return ErrInvalidInput
	}
	hash, err := hashPassword(password, service.random)
	if err != nil {
		return err
	}
	now := service.now().UTC()
	return service.store.WithinTransaction(
		ctx,
		func(repositories storage.Repositories) error {
			if err := repositories.Credentials().UpdatePassword(ctx, identityID, hash, now); err != nil {
				return err
			}
			_, err := repositories.OAuthSessions().
				RevokeIdentity(ctx, identityID, now)
			return err
		},
	)
}
