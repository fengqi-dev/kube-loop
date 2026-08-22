package oauthserver

import (
	"context"
	"errors"
	"time"

	"github.com/ory/fosite"

	controlstorage "github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

func (storage *Storage) CreateAccessTokenSession(
	ctx context.Context,
	signature string,
	request fosite.Requester,
) error {
	return storage.create(
		ctx,
		kindAccessToken,
		signature,
		request,
		fosite.AccessToken,
	)
}

func (storage *Storage) GetAccessTokenSession(
	ctx context.Context,
	signature string,
	_ fosite.Session,
) (fosite.Requester, error) {
	return storage.get(ctx, kindAccessToken, signature, nil)
}

func (storage *Storage) DeleteAccessTokenSession(
	ctx context.Context,
	signature string,
) error {
	return storage.repositoriesFor(ctx).
		OAuthSessions().
		Delete(ctx, kindAccessToken, signatureHash(signature))
}

func (storage *Storage) CreateRefreshTokenSession(
	ctx context.Context,
	signature, _ string,
	request fosite.Requester,
) error {
	return storage.create(
		ctx,
		kindRefreshToken,
		signature,
		request,
		fosite.RefreshToken,
	)
}

func (storage *Storage) GetRefreshTokenSession(
	ctx context.Context,
	signature string,
	_ fosite.Session,
) (fosite.Requester, error) {
	return storage.get(
		ctx,
		kindRefreshToken,
		signature,
		fosite.ErrInactiveToken,
	)
}

func (storage *Storage) DeleteRefreshTokenSession(
	ctx context.Context,
	signature string,
) error {
	return storage.repositoriesFor(ctx).
		OAuthSessions().
		Delete(ctx, kindRefreshToken, signatureHash(signature))
}

func (storage *Storage) RotateRefreshToken(
	ctx context.Context,
	requestID, signature string,
) error {
	_, err := storage.repositoriesFor(ctx).
		OAuthSessions().
		Consume(ctx, kindRefreshToken, signatureHash(signature), time.Now().UTC())
	if errors.Is(err, controlstorage.ErrNotFound) {
		_ = storage.repositoriesFor(ctx).
			OAuthSessions().
			RevokeRequest(ctx, requestID, time.Now().UTC())
		return fosite.ErrInactiveToken
	}
	return err
}

func (storage *Storage) RevokeRefreshToken(
	ctx context.Context,
	requestID string,
) error {
	return storage.repositoriesFor(ctx).
		OAuthSessions().
		RevokeRequest(ctx, requestID, time.Now().UTC())
}

func (storage *Storage) RevokeAccessToken(
	ctx context.Context,
	requestID string,
) error {
	return storage.repositoriesFor(ctx).
		OAuthSessions().
		RevokeRequest(ctx, requestID, time.Now().UTC())
}
