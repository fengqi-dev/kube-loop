package storage

import (
	"context"
	"errors"
	"strings"
	"time"
)

func (repository *oauthSessionRepository) Consume(
	ctx context.Context,
	kind string,
	hash []byte,
	now time.Time,
) (OAuthSession, error) {
	result, err := repository.executor.ExecContext(
		ctx,
		repository.bind(
			`UPDATE oauth_sessions SET status = 'consumed' WHERE kind = ? AND signature_hash = ?`+
				` AND status = 'active' AND expires_at > ?`,
		),
		kind,
		hash,
		formatTime(now),
	)
	if err != nil {
		return OAuthSession{}, mapWriteError(err)
	}
	count, err := rowsAffected(result)
	if err != nil {
		return OAuthSession{}, err
	}
	if count != 1 {
		return OAuthSession{}, ErrNotFound
	}
	return repository.get(ctx, kind, hash)
}

func (repository *oauthSessionRepository) Delete(
	ctx context.Context,
	kind string,
	hash []byte,
) error {
	_, err := repository.executor.ExecContext(
		ctx,
		repository.bind(
			`DELETE FROM oauth_sessions WHERE kind = ? AND signature_hash = ?`,
		),
		kind,
		hash,
	)
	return mapWriteError(err)
}

func (repository *oauthSessionRepository) RevokeRequest(
	ctx context.Context,
	requestID string,
	now time.Time,
) error {
	_, err := repository.executor.ExecContext(
		ctx,
		repository.bind(
			`UPDATE oauth_sessions SET status = 'revoked', revoked_at = ? WHERE request_id = ? AND status = 'active'`,
		),
		formatTime(now),
		requestID,
	)
	if err != nil {
		return mapWriteError(err)
	}
	_, err = repository.executor.ExecContext(
		ctx,
		repository.bind(
			`UPDATE admin_sessions SET revoked_at = COALESCE(revoked_at, ?) WHERE authorization_id = ?`,
		),
		formatTime(now),
		requestID,
	)
	return mapWriteError(err)
}

func (repository *oauthSessionRepository) RevokeIdentity(
	ctx context.Context,
	identityID string,
	now time.Time,
) (int64, error) {
	if strings.TrimSpace(identityID) == "" || now.IsZero() {
		return 0, errors.New("oAuth identity revocation is invalid")
	}
	result, err := repository.executor.ExecContext(
		ctx,
		repository.bind(
			`UPDATE oauth_sessions SET status = 'revoked', revoked_at = ?
		WHERE identity_id = ? AND status = 'active'`,
		),
		formatTime(now),
		identityID,
	)
	if err != nil {
		return 0, mapWriteError(err)
	}
	return rowsAffected(result)
}
