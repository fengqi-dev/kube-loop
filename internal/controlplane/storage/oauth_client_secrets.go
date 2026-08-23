package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

func (repository *oauthClientRepository) SetSecret(
	ctx context.Context,
	secret OAuthClientSecret,
) error {
	if strings.TrimSpace(secret.ClientID) == "" || len(secret.SecretHash) < 32 {
		return errors.New("oAuth client secret hash is invalid")
	}
	if secret.CreatedAt.IsZero() {
		secret.CreatedAt = time.Now()
	}
	if secret.UpdatedAt.IsZero() {
		secret.UpdatedAt = secret.CreatedAt
	}
	query := `INSERT INTO oauth_client_secrets(client_id, secret_hash, created_at, updated_at)
		VALUES (?, ?, ?, ?)` +
		` ON CONFLICT(client_id) DO UPDATE SET secret_hash=excluded.secret_hash, updated_at=excluded.updated_at`
	if repository.backend == BackendPostgreSQL {
		query = `INSERT INTO oauth_client_secrets(client_id, secret_hash, created_at, updated_at)
		VALUES ($1, $2, $3, $4)` +
			` ON CONFLICT(client_id) DO UPDATE SET secret_hash=excluded.secret_hash, updated_at=excluded.updated_at`
	}
	if repository.backend == BackendMySQL {
		query = `INSERT INTO oauth_client_secrets(client_id, secret_hash, created_at, updated_at)
		VALUES (?, ?, ?, ?) ON DUPLICATE KEY UPDATE secret_hash=VALUES(secret_hash), updated_at=VALUES(updated_at)`
	}
	_, err := repository.executor.ExecContext(
		ctx,
		query,
		secret.ClientID,
		secret.SecretHash,
		formatTime(secret.CreatedAt),
		formatTime(secret.UpdatedAt),
	)
	return mapWriteError(err)
}

func (repository *oauthClientRepository) GetSecret(
	ctx context.Context,
	clientID string,
) (OAuthClientSecret, error) {
	var value OAuthClientSecret
	var created, updated string
	query := repository.bind(
		`SELECT client_id, secret_hash, created_at, updated_at FROM oauth_client_secrets WHERE client_id = ?`,
	)
	err := repository.executor.QueryRowContext(ctx, query, clientID).
		Scan(&value.ClientID, &value.SecretHash, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return OAuthClientSecret{}, ErrNotFound
	}
	if err != nil {
		return OAuthClientSecret{}, databaseError(
			"read OAuth client secret",
			err,
		)
	}
	value.CreatedAt, err = parseTime(
		created,
		"OAuth client secret creation time",
	)
	if err == nil {
		value.UpdatedAt, err = parseTime(
			updated,
			"OAuth client secret update time",
		)
	}
	return value, err
}
