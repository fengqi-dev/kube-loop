package storage

import (
	"context"
	"errors"
	"strings"
	"time"
)

func (repository *adminSessionRepository) Revoke(
	ctx context.Context,
	idHash []byte,
	revokedAt time.Time,
) error {
	if len(idHash) != sha256Size || revokedAt.IsZero() {
		return errors.New(
			"management session hash and revocation time are required",
		)
	}
	query := repository.bind(
		`UPDATE admin_sessions SET revoked_at = COALESCE(revoked_at, ?) WHERE id_hash = ?`,
	)
	result, err := repository.executor.ExecContext(
		ctx,
		query,
		formatTime(revokedAt),
		idHash,
	)
	if err != nil {
		return mapWriteError(err)
	}
	count, err := rowsAffected(result)
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

func (repository *adminSessionRepository) RevokeAuthorization(
	ctx context.Context,
	authorizationID string,
	revokedAt time.Time,
) error {
	if strings.TrimSpace(authorizationID) == "" || revokedAt.IsZero() {
		return errors.New(
			"management authorization and revocation time are required",
		)
	}
	_, err := repository.executor.ExecContext(
		ctx,
		repository.bind(
			`UPDATE admin_sessions SET revoked_at = COALESCE(revoked_at, ?) WHERE authorization_id = ?`,
		),
		formatTime(revokedAt),
		authorizationID,
	)
	return mapWriteError(err)
}

func (repository *adminSessionRepository) DeleteExpired(
	ctx context.Context,
	before time.Time,
	limit int,
) (int64, error) {
	limit, err := boundedLimit(limit)
	if err != nil {
		return 0, err
	}
	if before.IsZero() {
		return 0, errors.New("management session cleanup time is required")
	}
	query := `DELETE FROM admin_sessions WHERE rowid IN (
		SELECT rowid FROM admin_sessions
		WHERE idle_expires_at < ? OR absolute_expires_at < ? OR (revoked_at IS NOT NULL AND revoked_at < ?)
		ORDER BY absolute_expires_at LIMIT ?
	)`
	if repository.backend == BackendPostgreSQL {
		query = `DELETE FROM admin_sessions WHERE ctid IN (
			SELECT ctid FROM admin_sessions
			WHERE idle_expires_at < $1 OR absolute_expires_at < $2 OR (revoked_at IS NOT NULL AND revoked_at < $3)
			ORDER BY absolute_expires_at LIMIT $4
		)`
	}
	if repository.backend == BackendMySQL {
		query = `DELETE FROM admin_sessions
			WHERE idle_expires_at < ? OR absolute_expires_at < ? OR (revoked_at IS NOT NULL AND revoked_at < ?)
			ORDER BY absolute_expires_at LIMIT ?`
	}
	result, err := repository.executor.ExecContext(
		ctx,
		query,
		formatTime(before),
		formatTime(before),
		formatTime(before),
		limit,
	)
	if err != nil {
		return 0, databaseError("delete expired management sessions", err)
	}
	return rowsAffected(result)
}
