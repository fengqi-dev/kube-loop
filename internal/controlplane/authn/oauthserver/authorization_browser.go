package oauthserver

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/ory/fosite"

	controlstorage "github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

func (endpoints *Endpoints) AuthenticateLocal(
	ctx context.Context,
	username string,
	password []byte,
	requestID string,
) (BrowserIdentity, error) {
	if endpoints == nil || endpoints.repositories == nil ||
		endpoints.localAuth == nil {
		return BrowserIdentity{}, fosite.ErrServerError
	}
	identity, err := endpoints.localAuth(ctx, username, password, requestID)
	if err != nil {
		return BrowserIdentity{}, err
	}
	return BrowserIdentity{
		Identity:   identity,
		ProviderID: providerLocal,
		AuthTime:   time.Now().UTC(),
	}, nil
}

func (endpoints *Endpoints) CreateBrowserSession(
	ctx context.Context,
	identity BrowserIdentity,
	ttl time.Duration,
) (string, error) {
	if identity.Identity.ID == "" || identity.ProviderID != providerLocal ||
		ttl <= 0 {
		return "", fosite.ErrServerError
	}
	token, err := randomAuthorizationValue()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	err = endpoints.repositories.OAuthBrowserSessions().
		Create(ctx, controlstorage.OAuthBrowserSession{
			IDHash: signatureHash(
				token,
			), IdentityID: identity.Identity.ID, ProviderID: providerLocal,
			AuthTime: now, CreatedAt: now, ExpiresAt: now.Add(ttl),
		})
	return token, err
}

func (endpoints *Endpoints) BrowserIdentity(
	ctx context.Context,
	token string,
) (BrowserIdentity, error) {
	stored, err := endpoints.repositories.OAuthBrowserSessions().
		Get(ctx, signatureHash(token), time.Now().UTC())
	if err != nil {
		return BrowserIdentity{}, err
	}
	if stored.ProviderID != providerLocal {
		return BrowserIdentity{}, fosite.ErrNotFound
	}
	identity, err := endpoints.repositories.Identities().
		GetByID(ctx, stored.IdentityID)
	if err != nil || identity.Status != statusActive {
		return BrowserIdentity{}, fosite.ErrNotFound
	}
	return BrowserIdentity{
		Identity:   identity,
		ProviderID: providerLocal,
		AuthTime:   stored.AuthTime,
	}, nil
}

func (endpoints *Endpoints) RevokeBrowserSession(
	ctx context.Context,
	token string,
) error {
	if strings.TrimSpace(token) == "" {
		return nil
	}
	err := endpoints.repositories.OAuthBrowserSessions().
		Revoke(ctx, signatureHash(token), time.Now().UTC())
	if errors.Is(err, controlstorage.ErrNotFound) {
		return nil
	}
	return err
}
