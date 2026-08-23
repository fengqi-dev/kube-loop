package remote

import (
	"context"
	"errors"
	"fmt"

	clientauth "github.com/fengqi-dev/kube-loop/internal/client/auth"
	"github.com/fengqi-dev/kube-loop/internal/client/credentials"
	"github.com/fengqi-dev/kube-loop/internal/client/profile"
)

func (client *Client) usableCredential(
	ctx context.Context,
	serverProfile profile.Profile,
	rejectedAccessToken string,
) (credentials.Credential, error) {
	current, err := client.credentials.Get(serverProfile.ID)
	if err != nil {
		return credentials.Credential{}, err
	}
	if rejectedAccessToken == "" && current.AccessExpiresAt.After(client.now().Add(client.refreshAhead)) {
		return current, nil
	}
	client.refreshMu.Lock()
	defer client.refreshMu.Unlock()
	current, err = client.credentials.Get(serverProfile.ID)
	if err != nil {
		return credentials.Credential{}, err
	}
	if rejectedAccessToken != "" && current.AccessToken != rejectedAccessToken {
		return current, nil
	}
	if rejectedAccessToken == "" && current.AccessExpiresAt.After(client.now().Add(client.refreshAhead)) {
		return current, nil
	}
	if !current.RefreshExpiresAt.IsZero() && !current.RefreshExpiresAt.After(client.now()) {
		return credentials.Credential{}, client.expiredLogin(serverProfile.ID)
	}
	refreshed, err := client.refresher.Refresh(ctx, serverProfile.BaseURL, current)
	if err != nil {
		if clientauth.IsInvalidGrant(err) {
			return credentials.Credential{}, client.expiredLogin(serverProfile.ID)
		}
		return credentials.Credential{}, fmt.Errorf("refresh Gateway login: %w", err)
	}
	if err := client.credentials.Set(serverProfile.ID, refreshed); err != nil {
		return credentials.Credential{}, fmt.Errorf("store refreshed Gateway login: %w", err)
	}
	return refreshed, nil
}

func (client *Client) expiredLogin(profileID string) error {
	deleteErr := client.credentials.Delete(profileID)
	if deleteErr != nil && !errors.Is(deleteErr, credentials.ErrNotFound) {
		return errors.Join(
			clientauth.ErrLoginExpired,
			fmt.Errorf("clear expired Gateway login: %w", deleteErr),
		)
	}
	return clientauth.ErrLoginExpired
}
