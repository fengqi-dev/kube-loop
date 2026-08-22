package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (repository *oauthSessionRepository) RequestOwner(
	ctx context.Context,
	requestID string,
) (string, string, error) {
	var identityID, deviceID string
	query := repository.bind(`SELECT identity_id, device_id FROM oauth_sessions
		WHERE request_id = ? AND identity_id IS NOT NULL` +
		` ORDER BY CASE WHEN kind = 'refresh_token' THEN 0 ELSE 1 END LIMIT 1`)
	err := repository.executor.QueryRowContext(ctx, query, requestID).
		Scan(&identityID, &deviceID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", databaseError("read OAuth grant owner", err)
	}
	return identityID, deviceID, nil
}

func (repository *oauthSessionRepository) RequestActive(
	ctx context.Context,
	requestID string,
	now time.Time,
) (bool, error) {
	var count int
	query := repository.bind(
		`SELECT COUNT(*) FROM oauth_sessions WHERE request_id = ? AND status = 'active'` +
			` AND expires_at > ? AND kind IN ('access_token', 'refresh_token')`,
	)
	err := repository.executor.QueryRowContext(ctx, query, requestID, formatTime(now)).
		Scan(&count)
	if err != nil {
		return false, databaseError("read OAuth grant state", err)
	}
	return count > 0, nil
}

func (repository *oauthSessionRepository) DeleteExpired(
	ctx context.Context,
	now time.Time,
	limit int,
) (int64, error) {
	if _, err := boundedLimit(limit); err != nil {
		return 0, err
	}
	result, err := repository.executor.ExecContext(
		ctx,
		repository.bind(`DELETE FROM oauth_sessions WHERE expires_at <= ?`),
		formatTime(now),
	)
	if err != nil {
		return 0, mapWriteError(err)
	}
	return rowsAffected(result)
}
