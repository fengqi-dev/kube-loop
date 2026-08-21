package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type idempotencyRepository struct {
	repositoryBase
}

func (repository *idempotencyRepository) Reserve(
	ctx context.Context,
	record IdempotencyRecord,
) (IdempotencyRecord, bool, error) {
	if err := normalizeIdempotencyRecord(&record); err != nil {
		return IdempotencyRecord{}, false, err
	}
	query := repository.bind(`INSERT INTO idempotency_records(
		scope, key, request_hash, resource_type, resource_id,
		response_json, created_at, expires_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(scope, key) DO NOTHING`)
	if repository.backend == BackendPostgreSQL {
		query = `INSERT INTO idempotency_records(
			scope, key, request_hash, resource_type, resource_id,
			response_json, created_at, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8)
		ON CONFLICT(scope, key) DO NOTHING`
	}
	if repository.backend == BackendMySQL {
		query = `INSERT IGNORE INTO idempotency_records(
			scope, ` + "`key`" + `, request_hash, resource_type, resource_id,
			response_json, created_at, expires_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	}
	result, err := repository.executor.ExecContext(ctx, query,
		record.Scope, record.Key, record.RequestHash,
		record.ResourceType, record.ResourceID, nullableJSON(record.Response),
		formatTime(record.CreatedAt), formatTime(record.ExpiresAt),
	)
	if err != nil {
		return IdempotencyRecord{}, false, mapWriteError(err)
	}
	count, err := rowsAffected(result)
	if err != nil {
		return IdempotencyRecord{}, false, err
	}
	if count == 1 {
		return record, true, nil
	}
	existing, err := repository.Get(ctx, record.Scope, record.Key)
	if err != nil {
		return IdempotencyRecord{}, false, err
	}
	if existing.RequestHash != record.RequestHash {
		return IdempotencyRecord{}, false, ErrIdempotencyMismatch
	}
	return existing, false, nil
}

func (repository *idempotencyRepository) Get(
	ctx context.Context,
	scope, key string,
) (IdempotencyRecord, error) {
	scope = strings.TrimSpace(scope)
	key = strings.TrimSpace(key)
	if scope == "" || key == "" {
		return IdempotencyRecord{}, errors.New(
			"idempotency scope and key are required",
		)
	}
	query := repository.bind(`SELECT scope, key, request_hash, resource_type,
		resource_id, response_json, created_at, expires_at
		FROM idempotency_records WHERE scope = ? AND key = ?`)
	if repository.backend == BackendMySQL {
		query = "SELECT scope, `key`, request_hash, resource_type, resource_id, response_json, " +
			"created_at, expires_at FROM idempotency_records WHERE scope = ? AND `key` = ?"
	}
	record, err := scanIdempotencyRecord(
		repository.executor.QueryRowContext(ctx, query, scope, key),
	)
	if errors.Is(err, sql.ErrNoRows) {
		return IdempotencyRecord{}, ErrNotFound
	}
	if err != nil {
		return IdempotencyRecord{}, databaseError(
			"read idempotency record",
			err,
		)
	}
	return record, nil
}

func (repository *idempotencyRepository) DeleteExpired(
	ctx context.Context,
	before time.Time,
	limit int,
) (int64, error) {
	limit, err := boundedLimit(limit)
	if err != nil {
		return 0, err
	}
	query := `DELETE FROM idempotency_records WHERE rowid IN (
		SELECT rowid FROM idempotency_records WHERE expires_at < ? ORDER BY expires_at LIMIT ?
	)`
	if repository.backend == BackendPostgreSQL {
		query = `DELETE FROM idempotency_records WHERE ctid IN (
			SELECT ctid FROM idempotency_records WHERE expires_at < $1 ORDER BY expires_at LIMIT $2
		)`
	}
	if repository.backend == BackendMySQL {
		query = `DELETE FROM idempotency_records WHERE expires_at < ? ORDER BY expires_at LIMIT ?`
	}
	result, err := repository.executor.ExecContext(
		ctx,
		query,
		formatTime(before),
		limit,
	)
	if err != nil {
		return 0, databaseError("delete expired idempotency records", err)
	}
	return rowsAffected(result)
}

func normalizeIdempotencyRecord(record *IdempotencyRecord) error {
	record.Scope = strings.TrimSpace(record.Scope)
	record.Key = strings.TrimSpace(record.Key)
	record.RequestHash = strings.TrimSpace(record.RequestHash)
	record.ResourceType = strings.TrimSpace(record.ResourceType)
	record.ResourceID = strings.TrimSpace(record.ResourceID)
	if record.Scope == "" || record.Key == "" || record.RequestHash == "" ||
		record.ResourceType == "" || record.ResourceID == "" {
		return errors.New(
			"idempotency scope, key, request hash and resource identity are required",
		)
	}
	var err error
	if record.Response, err = normalizeJSON(record.Response, false, "idempotency response"); err != nil {
		return err
	}
	if record.CreatedAt.IsZero() || record.ExpiresAt.IsZero() ||
		!record.ExpiresAt.After(record.CreatedAt) {
		return errors.New("idempotency expiry must be after creation")
	}
	record.CreatedAt = record.CreatedAt.UTC()
	record.ExpiresAt = record.ExpiresAt.UTC()
	return nil
}

func scanIdempotencyRecord(row rowScanner) (IdempotencyRecord, error) {
	var (
		record               IdempotencyRecord
		response             []byte
		createdAt, expiresAt string
	)
	if err := row.Scan(
		&record.Scope, &record.Key, &record.RequestHash,
		&record.ResourceType, &record.ResourceID, &response, &createdAt, &expiresAt,
	); err != nil {
		return IdempotencyRecord{}, err
	}
	if len(response) > 0 {
		record.Response = append(json.RawMessage(nil), response...)
	}
	var err error
	if record.CreatedAt, err = parseTime(createdAt, "idempotency creation time"); err != nil {
		return IdempotencyRecord{}, err
	}
	if record.ExpiresAt, err = parseTime(expiresAt, "idempotency expiry"); err != nil {
		return IdempotencyRecord{}, err
	}
	return record, nil
}
