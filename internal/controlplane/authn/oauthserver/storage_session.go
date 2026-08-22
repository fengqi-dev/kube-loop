package oauthserver

import (
	"context"
	"errors"
	"time"

	"github.com/ory/fosite"

	controlstorage "github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

func (storage *Storage) create(
	ctx context.Context,
	kind, signature string,
	request fosite.Requester,
	tokenType fosite.TokenType,
) error {
	raw, err := encodeRequester(request)
	if err != nil {
		return err
	}
	expires := request.GetSession().GetExpiresAt(tokenType)
	if expires.IsZero() {
		expires = time.Now().UTC().Add(10 * time.Minute)
	}
	session, _ := request.GetSession().(*Session)
	identityID, deviceID := "", ""
	if session != nil {
		identityID, deviceID = session.IdentityID, session.DeviceID
	}
	return storage.repositoriesFor(ctx).
		OAuthSessions().
		Create(ctx, controlstorage.OAuthSession{Kind: kind,
			SignatureHash: signatureHash(
				signature,
			), RequestID: request.GetID(), IdentityID: identityID,
			ClientID: request.GetClient().
				GetID(),
			DeviceID: deviceID, RequestJSON: raw, Status: statusActive,
			CreatedAt: time.Now().UTC(), ExpiresAt: expires})
}

func (storage *Storage) get(
	ctx context.Context,
	kind, signature string,
	invalidatedError error,
) (fosite.Requester, error) {
	stored, err := storage.repositoriesFor(ctx).
		OAuthSessions().
		Get(ctx, kind, signatureHash(signature))
	if errors.Is(err, controlstorage.ErrNotFound) {
		return nil, fosite.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	request, err := storage.decodeRequester(ctx, stored.RequestJSON)
	if err != nil {
		return nil, err
	}
	if stored.Status != statusActive ||
		!stored.ExpiresAt.After(time.Now().UTC()) {
		if invalidatedError != nil {
			return request, invalidatedError
		}
		return nil, fosite.ErrNotFound
	}
	return request, nil
}
