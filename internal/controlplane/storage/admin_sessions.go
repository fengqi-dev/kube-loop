package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

const sha256Size = 32

type adminSessionRepository struct {
	repositoryBase
}

var _ AdminSessionRepository = (*adminSessionRepository)(nil)

func (repository *adminSessionRepository) Create(ctx context.Context, session AdminSession) error {
	if err := normalizeAdminSession(&session); err != nil {
		return err
	}
	query := repository.bind(`INSERT INTO admin_sessions (
		id_hash, identity_id, authorization_id, authentication_type,
		csrf_token_hash, authenticated_at, created_at, last_seen_at,
		idle_expires_at, absolute_expires_at, revoked_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err := repository.executor.ExecContext(ctx, query,
		session.IDHash, nullableString(session.IdentityID), nullableString(session.AuthorizationID),
		session.AuthenticationType, session.CSRFTokenHash,
		formatTime(session.AuthenticatedAt), formatTime(session.CreatedAt), formatTime(session.LastSeenAt), formatTime(session.IdleExpiresAt),
		formatTime(session.AbsoluteExpiresAt), nullableTime(session.RevokedAt),
	)
	return mapWriteError(err)
}

func (repository *adminSessionRepository) GetByHash(ctx context.Context, idHash []byte) (AdminSession, error) {
	if len(idHash) != sha256Size {
		return AdminSession{}, errors.New("management session hash must be a SHA-256 value")
	}
	query := repository.bind(`SELECT
		id_hash, identity_id, authorization_id, authentication_type,
		csrf_token_hash, authenticated_at, created_at, last_seen_at,
		idle_expires_at, absolute_expires_at, revoked_at
		FROM admin_sessions WHERE id_hash = ?`)
	session, err := scanAdminSession(repository.executor.QueryRowContext(ctx, query, idHash))
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
	if len(idHash) != sha256Size || observedLastSeen.IsZero() || now.IsZero() || nextLastSeen.IsZero() || nextIdleExpiry.IsZero() ||
		nextLastSeen.Before(now) || !nextIdleExpiry.After(nextLastSeen) {
		return errors.New("management session touch values are invalid")
	}
	query := repository.bind(`UPDATE admin_sessions SET last_seen_at = ?, idle_expires_at = ?
		WHERE id_hash = ? AND last_seen_at = ? AND revoked_at IS NULL
		AND idle_expires_at > ? AND absolute_expires_at > ? AND absolute_expires_at >= ?`)
	result, err := repository.executor.ExecContext(ctx, query,
		formatTime(nextLastSeen), formatTime(nextIdleExpiry), idHash, formatTime(observedLastSeen),
		formatTime(now), formatTime(now), formatTime(nextIdleExpiry),
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

func (repository *adminSessionRepository) Revoke(ctx context.Context, idHash []byte, revokedAt time.Time) error {
	if len(idHash) != sha256Size || revokedAt.IsZero() {
		return errors.New("management session hash and revocation time are required")
	}
	query := repository.bind(`UPDATE admin_sessions SET revoked_at = COALESCE(revoked_at, ?) WHERE id_hash = ?`)
	result, err := repository.executor.ExecContext(ctx, query, formatTime(revokedAt), idHash)
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

func (repository *adminSessionRepository) RevokeAuthorization(ctx context.Context, authorizationID string, revokedAt time.Time) error {
	if strings.TrimSpace(authorizationID) == "" || revokedAt.IsZero() {
		return errors.New("management authorization and revocation time are required")
	}
	_, err := repository.executor.ExecContext(ctx, repository.bind(`UPDATE admin_sessions SET revoked_at = COALESCE(revoked_at, ?) WHERE authorization_id = ?`), formatTime(revokedAt), authorizationID)
	return mapWriteError(err)
}

func (repository *adminSessionRepository) DeleteExpired(ctx context.Context, before time.Time, limit int) (int64, error) {
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
	result, err := repository.executor.ExecContext(ctx, query, formatTime(before), formatTime(before), formatTime(before), limit)
	if err != nil {
		return 0, databaseError("delete expired management sessions", err)
	}
	return rowsAffected(result)
}

func normalizeAdminSession(session *AdminSession) error {
	session.IdentityID = strings.TrimSpace(session.IdentityID)
	session.AuthorizationID = strings.TrimSpace(session.AuthorizationID)
	session.AuthenticationType = strings.TrimSpace(session.AuthenticationType)
	if len(session.IDHash) != sha256Size || len(session.CSRFTokenHash) != sha256Size {
		return errors.New("management Session and CSRF hashes must be SHA-256 values")
	}
	switch session.AuthenticationType {
	case "normal", "bootstrap":
		if validateUUID(session.IdentityID, "management session identity ID") != nil ||
			validateUUID(session.AuthorizationID, "management session authorization ID") != nil {
			return errors.New("authenticated management session identity is invalid")
		}
	default:
		return errors.New("management session authentication type is invalid")
	}
	if session.AuthenticatedAt.IsZero() {
		session.AuthenticatedAt = session.CreatedAt
	}
	if session.CreatedAt.IsZero() || session.LastSeenAt.IsZero() ||
		session.IdleExpiresAt.IsZero() || session.AbsoluteExpiresAt.IsZero() || session.LastSeenAt.Before(session.CreatedAt) ||
		!session.IdleExpiresAt.After(session.LastSeenAt) || session.IdleExpiresAt.After(session.AbsoluteExpiresAt) {
		return errors.New("management session schema or lifetime is invalid")
	}
	session.IDHash = append([]byte(nil), session.IDHash...)
	session.CSRFTokenHash = append([]byte(nil), session.CSRFTokenHash...)
	session.CreatedAt = session.CreatedAt.UTC()
	session.AuthenticatedAt = session.AuthenticatedAt.UTC()
	session.LastSeenAt = session.LastSeenAt.UTC()
	session.IdleExpiresAt = session.IdleExpiresAt.UTC()
	session.AbsoluteExpiresAt = session.AbsoluteExpiresAt.UTC()
	if session.RevokedAt != nil {
		value := session.RevokedAt.UTC()
		session.RevokedAt = &value
	}
	return nil
}

func scanAdminSession(row rowScanner) (AdminSession, error) {
	var session AdminSession
	var identityID, authorizationID, revokedAt sql.NullString
	var authenticatedAt, createdAt, lastSeenAt, idleExpiresAt, absoluteExpiresAt string
	if err := row.Scan(
		&session.IDHash, &identityID, &authorizationID, &session.AuthenticationType,
		&session.CSRFTokenHash, &authenticatedAt, &createdAt, &lastSeenAt,
		&idleExpiresAt, &absoluteExpiresAt, &revokedAt,
	); err != nil {
		return AdminSession{}, err
	}
	session.IdentityID = identityID.String
	session.AuthorizationID = authorizationID.String
	var err error
	if session.AuthenticatedAt, err = parseTime(authenticatedAt, "management session authentication time"); err != nil {
		return AdminSession{}, err
	}
	if session.CreatedAt, err = parseTime(createdAt, "management session creation time"); err != nil {
		return AdminSession{}, err
	}
	if session.LastSeenAt, err = parseTime(lastSeenAt, "management session last-seen time"); err != nil {
		return AdminSession{}, err
	}
	if session.IdleExpiresAt, err = parseTime(idleExpiresAt, "management session idle expiry"); err != nil {
		return AdminSession{}, err
	}
	if session.AbsoluteExpiresAt, err = parseTime(absoluteExpiresAt, "management session absolute expiry"); err != nil {
		return AdminSession{}, err
	}
	if session.RevokedAt, err = parseNullableTime(revokedAt, "management session revocation time"); err != nil {
		return AdminSession{}, err
	}
	return session, nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
