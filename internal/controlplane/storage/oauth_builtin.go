package storage

import (
	"context"
	"errors"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/authconfig"
)

const (
	DesktopOAuthClientID    = authconfig.DesktopClientID
	TUIOAuthClientID        = authconfig.TUIClientID
	ManagementOAuthClientID = "kubeloop-management"
	DesktopOAuthRedirectURI = authconfig.DesktopRedirectURI
	TUIOAuthRedirectURI     = authconfig.TUIRedirectURI
)

// EnsureBuiltinOAuthClients creates the first-party public clients without
// overwriting an existing administrator-visible record. Built-in constraints
// are also enforced by OAuthClientRepository.Update and Delete.
func EnsureBuiltinOAuthClients(
	ctx context.Context,
	repository OAuthClientRepository,
	managementRedirectURI string,
) error {
	if repository == nil {
		return errors.New("oAuth client repository is required")
	}
	now := time.Now().UTC()
	clients := []OAuthClient{
		{
			ID: DesktopOAuthClientID, Name: "KubeLoop Desktop", Public: true,
			RedirectURIs: []string{DesktopOAuthRedirectURI},
			GrantTypes:   []string{grantAuthorizationCode, grantRefreshToken},
			Scopes: []string{
				scopeOpenID,
				scopeProfile,
				emailField,
				scopeOfflineAccess,
				scopeKubeLoopAPI,
			},
			Trusted: true, Enabled: true, Builtin: true, CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: TUIOAuthClientID, Name: "KubeLoop TUI", Public: true,
			RedirectURIs: []string{TUIOAuthRedirectURI},
			GrantTypes:   []string{grantAuthorizationCode, grantRefreshToken},
			Scopes: []string{
				scopeOpenID,
				scopeProfile,
				emailField,
				scopeOfflineAccess,
				scopeKubeLoopAPI,
			},
			Trusted: true, Enabled: true, Builtin: true, CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: ManagementOAuthClientID, Name: "KubeLoop Management", Public: true,
			RedirectURIs: []string{managementRedirectURI},
			GrantTypes:   []string{grantAuthorizationCode, grantRefreshToken},
			Scopes: []string{
				scopeOpenID,
				scopeProfile,
				emailField,
				scopeOfflineAccess,
				scopeKubeLoopAPI,
			},
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
		// Built-in clients always retain protected first-party semantics and
		// cannot retain risky grants.
		client.CreatedAt = stored.CreatedAt
		if err := repository.Update(ctx, client); err != nil {
			return err
		}
	}
	return nil
}
