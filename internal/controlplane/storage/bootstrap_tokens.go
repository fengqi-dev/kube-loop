package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type bootstrapTokenRepository struct{ repositoryBase }

func (repository *bootstrapTokenRepository) Create(
	ctx context.Context,
	token BootstrapToken,
) error {
	if len(token.TokenHash) != 32 || token.CreatedAt.IsZero() ||
		!token.ExpiresAt.After(token.CreatedAt) {
		return errors.New("bootstrap token is invalid")
	}
	_, err := repository.executor.ExecContext(
		ctx,
		repository.bind(`INSERT INTO bootstrap_tokens
		(id, token_hash, created_at, expires_at, consumed_at) VALUES (1, ?, ?, ?, NULL)`),
		token.TokenHash,
		formatTime(token.CreatedAt),
		formatTime(token.ExpiresAt),
	)
	return mapWriteError(err)
}

func (repository *bootstrapTokenRepository) Get(
	ctx context.Context,
) (BootstrapToken, error) {
	var token BootstrapToken
	var createdAt, expiresAt string
	var consumedAt sql.NullString
	err := repository.executor.QueryRowContext(ctx, `SELECT token_hash, created_at, expires_at, consumed_at
		FROM bootstrap_tokens WHERE id = 1`).
		Scan(
			&token.TokenHash, &createdAt, &expiresAt, &consumedAt,
		)
	if errors.Is(err, sql.ErrNoRows) {
		return BootstrapToken{}, ErrNotFound
	}
	if err != nil {
		return BootstrapToken{}, databaseError("read bootstrap token", err)
	}
	token.CreatedAt, err = parseTime(createdAt, "bootstrap token creation time")
	if err == nil {
		token.ExpiresAt, err = parseTime(
			expiresAt,
			"bootstrap token expiration time",
		)
	}
	if err == nil && consumedAt.Valid {
		value, parseErr := parseTime(
			consumedAt.String,
			"bootstrap token consumption time",
		)
		err = parseErr
		token.ConsumedAt = &value
	}
	return token, err
}

func (repository *bootstrapTokenRepository) Consume(
	ctx context.Context,
	hash []byte,
	now time.Time,
) error {
	if len(hash) != 32 || now.IsZero() {
		return errors.New("bootstrap token is invalid")
	}
	result, err := repository.executor.ExecContext(
		ctx,
		repository.bind(`UPDATE bootstrap_tokens SET consumed_at = ?
		WHERE token_hash = ? AND consumed_at IS NULL AND expires_at > ?`),
		formatTime(now),
		hash,
		formatTime(now),
	)
	if err != nil {
		return mapWriteError(err)
	}
	count, err := rowsAffected(result)
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrNotFound
	}
	return nil
}
