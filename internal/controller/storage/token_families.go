package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

type tokenFamilyRepository struct {
	repositoryBase
}

func (repository *tokenFamilyRepository) Create(ctx context.Context, family TokenFamily) error {
	if err := normalizeTokenFamily(&family); err != nil {
		return err
	}
	query := repository.bind(`INSERT INTO token_families(
		id, schema_version, principal_id, device_id, refresh_token_hash, created_at, expires_at, revoked_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err := repository.executor.ExecContext(ctx, query,
		family.ID, family.SchemaVersion, family.PrincipalID, family.DeviceID,
		family.RefreshTokenHash, formatTime(family.CreatedAt), formatTime(family.ExpiresAt), nullableTime(family.RevokedAt),
	)
	return mapWriteError(err)
}

func (repository *tokenFamilyRepository) GetByID(ctx context.Context, id string) (TokenFamily, error) {
	if err := validateUUID(id, "token family ID"); err != nil {
		return TokenFamily{}, err
	}
	query := repository.bind(`SELECT id, schema_version, principal_id, device_id, refresh_token_hash, created_at, expires_at, revoked_at
		FROM token_families WHERE id = ?`)
	family, err := scanTokenFamily(repository.executor.QueryRowContext(ctx, query, id))
	if errors.Is(err, sql.ErrNoRows) {
		return TokenFamily{}, ErrNotFound
	}
	if err != nil {
		return TokenFamily{}, databaseError("read token family", err)
	}
	return family, nil
}

func (repository *tokenFamilyRepository) Revoke(ctx context.Context, id string, revokedAt time.Time) error {
	if err := validateUUID(id, "token family ID"); err != nil {
		return err
	}
	if revokedAt.IsZero() {
		return errors.New("revocation time is required")
	}
	query := repository.bind(`UPDATE token_families SET revoked_at = COALESCE(revoked_at, ?) WHERE id = ?`)
	result, err := repository.executor.ExecContext(ctx, query, formatTime(revokedAt), id)
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

func (repository *tokenFamilyRepository) RevokeByPrincipal(
	ctx context.Context,
	principalID string,
	revokedAt time.Time,
) (int64, error) {
	if err := validateUUID(principalID, "principal ID"); err != nil {
		return 0, err
	}
	if revokedAt.IsZero() {
		return 0, errors.New("revocation time is required")
	}
	query := repository.bind(`UPDATE token_families SET revoked_at = ?
		WHERE principal_id = ? AND revoked_at IS NULL`)
	result, err := repository.executor.ExecContext(ctx, query, formatTime(revokedAt), principalID)
	if err != nil {
		return 0, mapWriteError(err)
	}
	return rowsAffected(result)
}

func (repository *tokenFamilyRepository) RotateHash(
	ctx context.Context,
	id string,
	expectedHash []byte,
	nextHash []byte,
) error {
	if err := validateUUID(id, "token family ID"); err != nil {
		return err
	}
	if len(expectedHash) != 32 || len(nextHash) != 32 {
		return errors.New("refresh token hashes must be SHA-256 values")
	}
	query := repository.bind(`UPDATE token_families SET refresh_token_hash = ?
		WHERE id = ? AND refresh_token_hash = ? AND revoked_at IS NULL`)
	result, err := repository.executor.ExecContext(ctx, query, nextHash, id, expectedHash)
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

func (repository *tokenFamilyRepository) DeleteExpired(ctx context.Context, before time.Time, limit int) (int64, error) {
	limit, err := boundedLimit(limit)
	if err != nil {
		return 0, err
	}
	query := `DELETE FROM token_families WHERE rowid IN (
		SELECT rowid FROM token_families WHERE expires_at < ? ORDER BY expires_at LIMIT ?
	)`
	if repository.backend == BackendPostgreSQL {
		query = `DELETE FROM token_families WHERE ctid IN (
			SELECT ctid FROM token_families WHERE expires_at < $1 ORDER BY expires_at LIMIT $2
		)`
	} else {
		query = repository.bind(query)
	}
	result, err := repository.executor.ExecContext(ctx, query, formatTime(before), limit)
	if err != nil {
		return 0, databaseError("delete expired token families", err)
	}
	return rowsAffected(result)
}

func normalizeTokenFamily(family *TokenFamily) error {
	if err := validateUUID(family.ID, "token family ID"); err != nil {
		return err
	}
	if err := validateUUID(family.PrincipalID, "principal ID"); err != nil {
		return err
	}
	family.DeviceID = strings.TrimSpace(family.DeviceID)
	if family.DeviceID == "" {
		return errors.New("device ID is required")
	}
	if len(family.RefreshTokenHash) < 32 {
		return errors.New("refresh token hash must contain at least 32 bytes")
	}
	if family.SchemaVersion == 0 {
		family.SchemaVersion = ObjectSchemaVersion
	}
	if family.SchemaVersion != ObjectSchemaVersion {
		return errors.New("unsupported token family schema version")
	}
	if family.CreatedAt.IsZero() || family.ExpiresAt.IsZero() || !family.ExpiresAt.After(family.CreatedAt) {
		return errors.New("token family expiry must be after creation")
	}
	family.CreatedAt = family.CreatedAt.UTC()
	family.ExpiresAt = family.ExpiresAt.UTC()
	if family.RevokedAt != nil {
		value := family.RevokedAt.UTC()
		family.RevokedAt = &value
	}
	return nil
}

func scanTokenFamily(row rowScanner) (TokenFamily, error) {
	var (
		family               TokenFamily
		createdAt, expiresAt string
		revokedAt            sql.NullString
	)
	if err := row.Scan(
		&family.ID, &family.SchemaVersion, &family.PrincipalID, &family.DeviceID,
		&family.RefreshTokenHash, &createdAt, &expiresAt, &revokedAt,
	); err != nil {
		return TokenFamily{}, err
	}
	var err error
	if family.CreatedAt, err = parseTime(createdAt, "token family creation time"); err != nil {
		return TokenFamily{}, err
	}
	if family.ExpiresAt, err = parseTime(expiresAt, "token family expiry"); err != nil {
		return TokenFamily{}, err
	}
	if family.RevokedAt, err = parseNullableTime(revokedAt, "token family revocation time"); err != nil {
		return TokenFamily{}, err
	}
	return family, nil
}
