package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const sha256Size = 32

type adminSessionRepository struct {
	repositoryBase
}

var _ AdminSessionRepository = (*adminSessionRepository)(nil)

func (repository *adminSessionRepository) Create(
	ctx context.Context,
	session AdminSession,
) error {
	if err := normalizeAdminSession(&session); err != nil {
		return err
	}
	query := repository.bind(`INSERT INTO admin_sessions (
		id_hash, identity_id, authorization_id, authentication_type,
		csrf_token_hash, authenticated_at, created_at, last_seen_at,
		idle_expires_at, absolute_expires_at, revoked_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err := repository.executor.ExecContext(
		ctx,
		query,
		session.IDHash,
		nullableString(session.IdentityID),
		nullableString(session.AuthorizationID),
		session.AuthenticationType,
		session.CSRFTokenHash,
		formatTime(
			session.AuthenticatedAt,
		),
		formatTime(session.CreatedAt),
		formatTime(session.LastSeenAt),
		formatTime(session.IdleExpiresAt),
		formatTime(session.AbsoluteExpiresAt),
		nullableTime(session.RevokedAt),
	)
	return mapWriteError(err)
}

func (repository *adminSessionRepository) GetByHash(
	ctx context.Context,
	idHash []byte,
) (AdminSession, error) {
	if len(idHash) != sha256Size {
		return AdminSession{}, errors.New(
			"management session hash must be a SHA-256 value",
		)
	}
	query := repository.bind(`SELECT
		id_hash, identity_id, authorization_id, authentication_type,
		csrf_token_hash, authenticated_at, created_at, last_seen_at,
		idle_expires_at, absolute_expires_at, revoked_at
		FROM admin_sessions WHERE id_hash = ?`)
	session, err := scanAdminSession(
		repository.executor.QueryRowContext(ctx, query, idHash),
	)
	if errors.Is(err, sql.ErrNoRows) {
		return AdminSession{}, ErrNotFound
	}
	if err != nil {
		return AdminSession{}, databaseError("read management session", err)
	}
	return session, nil
}

func (repository *adminSessionRepository) Touch(
	ctx context.Context,
	idHash []byte,
	observedLastSeen time.Time,
	now time.Time,
	nextLastSeen time.Time,
	nextIdleExpiry time.Time,
) error {
	if len(idHash) != sha256Size || observedLastSeen.IsZero() || now.IsZero() ||
		nextLastSeen.IsZero() ||
		nextIdleExpiry.IsZero() ||
		nextLastSeen.Before(now) ||
		!nextIdleExpiry.After(nextLastSeen) {
		return errors.New("management session touch values are invalid")
	}
	query := repository.bind(
		`UPDATE admin_sessions SET last_seen_at = ?, idle_expires_at = ?
		WHERE id_hash = ? AND last_seen_at = ? AND revoked_at IS NULL
		AND idle_expires_at > ? AND absolute_expires_at > ? AND absolute_expires_at >= ?`,
	)
	result, err := repository.executor.ExecContext(
		ctx,
		query,
		formatTime(
			nextLastSeen,
		),
		formatTime(nextIdleExpiry),
		idHash,
		formatTime(observedLastSeen),
		formatTime(now),
		formatTime(now),
		formatTime(nextIdleExpiry),
	)
	if err != nil {
		return mapWriteError(err)
	}
	count, err := rowsAffected(result)
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}
