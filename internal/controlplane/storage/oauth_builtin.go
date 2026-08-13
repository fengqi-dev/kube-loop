package storage

import (
	"context"
	"errors"
	"time"
)

const (
	DesktopOAuthClientID    = "kubeloop-desktop"
	ManagementOAuthClientID = "kubeloop-management"
)

// EnsureBuiltinOAuthClients creates the two first-party public clients without
// overwriting an existing administrator-visible record. Built-in constraints
// are also enforced by OAuthClientRepository.Update and Delete.
func EnsureBuiltinOAuthClients(ctx context.Context, repository OAuthClientRepository, managementRedirectURI string) error {
	if repository == nil {
		return errors.New("OAuth client repository is required")
	}
	now := time.Now().UTC()
	clients := []OAuthClient{
		{
			ID: DesktopOAuthClientID, Name: "KubeLoop Desktop", Public: true,
			RedirectURIs: []string{"http://127.0.0.1/callback"},
			GrantTypes:   []string{"authorization_code", "refresh_token"}, ResponseTypes: []string{"code"},
			Scopes:  []string{"openid", "profile", "email", "offline_access", "kubeloop.api"},
			Trusted: true, Enabled: true, Builtin: true, CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: ManagementOAuthClientID, Name: "KubeLoop Management", Public: true,
			RedirectURIs: []string{managementRedirectURI},
			GrantTypes:   []string{"authorization_code", "refresh_token"}, ResponseTypes: []string{"code"},
			Scopes:  []string{"openid", "profile", "email", "offline_access", "kubeloop.api"},
			Trusted: true, Enabled: true, Builtin: true, CreatedAt: now, UpdatedAt: now,
		},
	}
	for _, client := range clients {
		stored, err := repository.Get(ctx, client.ID)
		if errors.Is(err, ErrNotFound) {
			if err := repository.Create(ctx, client); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		// A pre-migration collision must be upgraded to protected first-party
		// semantics and cannot retain risky grants.
		client.CreatedAt = stored.CreatedAt
		if err := repository.Update(ctx, client); err != nil {
			return err
		}
	}
	return nil
}
