package oauthserver

import (
	"context"
	"errors"
	"time"

	"github.com/ory/fosite"

	controlstorage "github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

func (storage *Storage) CreateAuthorizeCodeSession(
	ctx context.Context,
	code string,
	request fosite.Requester,
) error {
	return storage.create(
		ctx,
		kindAuthorizationCode,
		code,
		request,
		fosite.AuthorizeCode,
	)
}

func (storage *Storage) GetAuthorizeCodeSession(
	ctx context.Context,
	code string,
	_ fosite.Session,
) (fosite.Requester, error) {
	return storage.get(
		ctx,
		kindAuthorizationCode,
		code,
		fosite.ErrInvalidatedAuthorizeCode,
	)
}

func (storage *Storage) InvalidateAuthorizeCodeSession(
	ctx context.Context,
	code string,
) error {
	_, err := storage.repositoriesFor(ctx).
		OAuthSessions().
		Consume(ctx, kindAuthorizationCode, signatureHash(code), time.Now().UTC())
	if errors.Is(err, controlstorage.ErrNotFound) {
		return fosite.ErrInvalidatedAuthorizeCode
	}
	return err
}
