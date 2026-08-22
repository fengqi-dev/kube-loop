package oauthserver

import (
	"context"

	"github.com/ory/fosite"
)

func (storage *Storage) CreatePKCERequestSession(
	ctx context.Context,
	signature string,
	request fosite.Requester,
) error {
	return storage.create(
		ctx,
		kindPKCE,
		signature,
		request,
		fosite.AuthorizeCode,
	)
}

func (storage *Storage) GetPKCERequestSession(
	ctx context.Context,
	signature string,
	_ fosite.Session,
) (fosite.Requester, error) {
	return storage.get(ctx, kindPKCE, signature, nil)
}

func (storage *Storage) DeletePKCERequestSession(
	ctx context.Context,
	signature string,
) error {
	return storage.repositoriesFor(ctx).
		OAuthSessions().
		Delete(ctx, kindPKCE, signatureHash(signature))
}

func (storage *Storage) CreateOpenIDConnectSession(
	ctx context.Context,
	code string,
	request fosite.Requester,
) error {
	return storage.create(ctx, kindOIDC, code, request, fosite.AuthorizeCode)
}

func (storage *Storage) GetOpenIDConnectSession(
	ctx context.Context,
	code string,
	_ fosite.Requester,
) (fosite.Requester, error) {
	return storage.get(ctx, kindOIDC, code, nil)
}

func (storage *Storage) DeleteOpenIDConnectSession(
	ctx context.Context,
	code string,
) error {
	return storage.repositoriesFor(ctx).
		OAuthSessions().
		Delete(ctx, kindOIDC, signatureHash(code))
}
