package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

type refreshTokenRepository struct {
	repositoryBase
}

func (repository *refreshTokenRepository) Create(ctx context.Context, record RefreshTokenRecord) error {
	if err := normalizeRefreshToken(&record); err != nil {
		return err
	}
	query := repository.bind(`INSERT INTO refresh_tokens(
		token_hash, family_id, status, created_at, used_at
	) VALUES (?, ?, ?, ?, ?)`)
	_, err := repository.executor.ExecContext(ctx, query,
		record.TokenHash, record.FamilyID, record.Status, formatTime(record.CreatedAt), nullableTime(record.UsedAt),
	)
	return mapWriteError(err)
}

func (repository *refreshTokenRepository) GetByHash(ctx context.Context, hash []byte) (RefreshTokenRecord, error) {
	if len(hash) != 32 {
		return RefreshTokenRecord{}, errors.New("refresh token hash must be a SHA-256 value")
	}
	query := repository.bind(`SELECT token_hash, family_id, status, created_at, used_at
		FROM refresh_tokens WHERE token_hash = ?`)
	record, err := scanRefreshToken(repository.executor.QueryRowContext(ctx, query, hash))
	if errors.Is(err, sql.ErrNoRows) {
		return RefreshTokenRecord{}, ErrNotFound
	}
	if err != nil {
		return RefreshTokenRecord{}, databaseError("read refresh token", err)
	}
	return record, nil
}

func (repository *refreshTokenRepository) MarkUsed(ctx context.Context, hash []byte, usedAt time.Time) error {
	if len(hash) != 32 || usedAt.IsZero() {
		return errors.New("refresh token hash and use time are required")
	}
	query := repository.bind(`UPDATE refresh_tokens SET status = 'used', used_at = ?
		WHERE token_hash = ? AND status = 'active'`)
	result, err := repository.executor.ExecContext(ctx, query, formatTime(usedAt), hash)
	if err != nil {
		return errors.New("mark refresh token used")
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

func normalizeRefreshToken(record *RefreshTokenRecord) error {
	record.FamilyID = strings.TrimSpace(record.FamilyID)
	record.Status = strings.TrimSpace(record.Status)
	if len(record.TokenHash) != 32 {
		return errors.New("refresh token hash must be a SHA-256 value")
	}
	if err := validateUUID(record.FamilyID, "refresh token family ID"); err != nil {
		return err
	}
	if record.Status == "" {
		record.Status = "active"
	}
	if record.Status != "active" && record.Status != "used" {
		return errors.New("refresh token status must be active or used")
	}
	if record.CreatedAt.IsZero() || (record.Status == "used" && record.UsedAt == nil) ||
		(record.Status == "active" && record.UsedAt != nil) {
		return errors.New("refresh token timestamps do not match status")
	}
	record.TokenHash = append([]byte(nil), record.TokenHash...)
	record.CreatedAt = record.CreatedAt.UTC()
	if record.UsedAt != nil {
		usedAt := record.UsedAt.UTC()
		record.UsedAt = &usedAt
	}
	return nil
}

func scanRefreshToken(row rowScanner) (RefreshTokenRecord, error) {
	var record RefreshTokenRecord
	var createdAt string
	var usedAt sql.NullString
	if err := row.Scan(&record.TokenHash, &record.FamilyID, &record.Status, &createdAt, &usedAt); err != nil {
		return RefreshTokenRecord{}, err
	}
	var err error
	if record.CreatedAt, err = parseTime(createdAt, "refresh token creation time"); err != nil {
		return RefreshTokenRecord{}, err
	}
	if usedAt.Valid {
		parsed, err := parseTime(usedAt.String, "refresh token use time")
		if err != nil {
			return RefreshTokenRecord{}, err
		}
		record.UsedAt = &parsed
	}
	return record, nil
}
