package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type oauthBrowserSessionRepository struct{ repositoryBase }

func (repository *oauthBrowserSessionRepository) Create(ctx context.Context, session OAuthBrowserSession) error {
	if len(session.IDHash) != sha256Size || session.IdentityID == "" || session.ProviderID == "" ||
		session.AuthTime.IsZero() || session.CreatedAt.IsZero() || !session.ExpiresAt.After(session.CreatedAt) {
		return errors.New("OAuth browser session is invalid")
	}
	_, err := repository.executor.ExecContext(ctx, repository.bind(`INSERT INTO oauth_browser_sessions(
		id_hash, identity_id, provider_id, auth_time, created_at, expires_at, revoked_at
	) VALUES (?, ?, ?, ?, ?, ?, ?)`), session.IDHash, session.IdentityID, session.ProviderID,
		formatTime(session.AuthTime), formatTime(session.CreatedAt), formatTime(session.ExpiresAt), nullableTime(session.RevokedAt))
	return mapWriteError(err)
}

func (repository *oauthBrowserSessionRepository) Get(ctx context.Context, hash []byte, now time.Time) (OAuthBrowserSession, error) {
	if len(hash) != sha256Size || now.IsZero() {
		return OAuthBrowserSession{}, ErrNotFound
	}
	var value OAuthBrowserSession
	var authTime, createdAt, expiresAt string
	var revokedAt sql.NullString
	err := repository.executor.QueryRowContext(ctx, repository.bind(`SELECT id_hash, identity_id, provider_id,
		auth_time, created_at, expires_at, revoked_at FROM oauth_browser_sessions
		WHERE id_hash = ? AND revoked_at IS NULL AND expires_at > ?`), hash, formatTime(now)).Scan(
		&value.IDHash, &value.IdentityID, &value.ProviderID, &authTime, &createdAt, &expiresAt, &revokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return OAuthBrowserSession{}, ErrNotFound
	}
	if err != nil {
		return OAuthBrowserSession{}, databaseError("read OAuth browser session", err)
	}
	value.AuthTime, err = parseTime(authTime, "OAuth browser session authentication time")
	if err == nil {
		value.CreatedAt, err = parseTime(createdAt, "OAuth browser session creation time")
	}
	if err == nil {
		value.ExpiresAt, err = parseTime(expiresAt, "OAuth browser session expiry")
	}
	if err == nil && revokedAt.Valid {
		var parsed time.Time
		parsed, err = parseTime(revokedAt.String, "OAuth browser session revocation time")
		value.RevokedAt = &parsed
	}
	return value, err
}

func (repository *oauthBrowserSessionRepository) Revoke(ctx context.Context, hash []byte, now time.Time) error {
	if len(hash) != sha256Size || now.IsZero() {
		return errors.New("OAuth browser session revocation is invalid")
	}
	result, err := repository.executor.ExecContext(ctx, repository.bind(`UPDATE oauth_browser_sessions
		SET revoked_at = COALESCE(revoked_at, ?) WHERE id_hash = ?`), formatTime(now), hash)
	if err != nil {
		return mapWriteError(err)
	}
	count, err := rowsAffected(result)
	if err == nil && count == 0 {
		return ErrNotFound
	}
	return err
}

func (repository *oauthBrowserSessionRepository) DeleteExpired(ctx context.Context, now time.Time, limit int) (int64, error) {
	if _, err := boundedLimit(limit); err != nil {
		return 0, err
	}
	result, err := repository.executor.ExecContext(ctx, repository.bind(`DELETE FROM oauth_browser_sessions WHERE expires_at <= ?`), formatTime(now))
	if err != nil {
		return 0, mapWriteError(err)
	}
	return rowsAffected(result)
}
