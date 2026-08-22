package localuser

import (
	"context"
	"errors"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

func (service *Service) List(ctx context.Context) ([]User, error) {
	identities, err := service.store.Identities().
		List(ctx, storage.IdentityListFilter{Type: "human", Limit: 100})
	if err != nil {
		return nil, err
	}
	users := make([]User, 0, len(identities))
	for _, identity := range identities {
		credential, credentialErr := service.store.Credentials().
			GetPasswordByIdentity(ctx, identity.ID)
		if errors.Is(credentialErr, storage.ErrNotFound) {
			continue
		}
		if credentialErr != nil {
			return nil, credentialErr
		}
		users = append(users, toUser(identity, credential))
	}
	return users, nil
}

func (service *Service) Get(
	ctx context.Context,
	identityID string,
) (User, error) {
	stored, err := service.store.Credentials().
		GetPasswordByIdentity(ctx, identityID)
	if err != nil {
		return User{}, err
	}
	return service.user(ctx, stored)
}

func (service *Service) user(
	ctx context.Context,
	stored storage.PasswordCredential,
) (User, error) {
	identity, err := service.store.Identities().GetByID(ctx, stored.IdentityID)
	if err != nil {
		return User{}, err
	}
	return toUser(identity, stored), nil
}

func toUser(identity storage.Identity, stored storage.PasswordCredential) User {
	return User{
		IdentityID:  stored.IdentityID,
		Username:    stored.Username,
		DisplayName: identity.DisplayName,
		Email:       identity.PrimaryEmail,
		Enabled:     stored.Enabled && identity.Status == "active",
		CreatedAt:   stored.CreatedAt,
		UpdatedAt:   stored.UpdatedAt,
	}
}
