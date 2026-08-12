package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

type authTransactionRepository struct {
	repositoryBase
}

func (repository *authTransactionRepository) CreateAttempt(ctx context.Context, attempt AuthAttempt) error {
	if err := normalizeAuthAttempt(&attempt); err != nil {
		return err
	}
	query := repository.bind(`INSERT INTO auth_attempts(
		id, schema_version, provider_id, state_hash, client_state, client_callback,
		client_id, scope, nonce, pkce_challenge, upstream_pkce_verifier, created_at, expires_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err := repository.executor.ExecContext(ctx, query,
		attempt.ID, attempt.SchemaVersion, attempt.ProviderID, attempt.StateHash,
		attempt.ClientState, attempt.ClientCallback, attempt.ClientID, attempt.Scope,
		attempt.Nonce, attempt.PKCEChallenge,
		attempt.UpstreamPKCEVerifier,
		formatTime(attempt.CreatedAt), formatTime(attempt.ExpiresAt),
	)
	return mapWriteError(err)
}

func (repository *authTransactionRepository) ConsumeAttempt(
	ctx context.Context,
	stateHash []byte,
	now time.Time,
) (AuthAttempt, error) {
	if len(stateHash) != 32 || now.IsZero() {
		return AuthAttempt{}, errors.New("state hash and current time are required")
	}
	query := repository.bind(`DELETE FROM auth_attempts
		WHERE state_hash = ? AND expires_at > ?
		RETURNING id, schema_version, provider_id, state_hash, client_state,
			client_callback, client_id, scope, nonce, pkce_challenge,
			upstream_pkce_verifier, created_at, expires_at`)
	attempt, err := scanAuthAttempt(repository.executor.QueryRowContext(ctx, query, stateHash, formatTime(now)))
	if errors.Is(err, sql.ErrNoRows) {
		return AuthAttempt{}, ErrNotFound
	}
	if err != nil {
		return AuthAttempt{}, databaseError("consume authentication attempt", err)
	}
	return attempt, nil
}

func (repository *authTransactionRepository) CreateExchange(ctx context.Context, exchange AuthExchange) error {
	if err := normalizeAuthExchange(&exchange); err != nil {
		return err
	}
	query := repository.bind(`INSERT INTO auth_exchanges(
		schema_version, code_hash, principal_id, provider_id, client_id, redirect_uri,
		scope, nonce, pkce_challenge, created_at, expires_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err := repository.executor.ExecContext(ctx, query,
		exchange.SchemaVersion, exchange.CodeHash, exchange.PrincipalID, exchange.ProviderID,
		exchange.ClientID, exchange.RedirectURI, exchange.Scope, exchange.Nonce,
		exchange.PKCEChallenge, formatTime(exchange.CreatedAt), formatTime(exchange.ExpiresAt),
	)
	return mapWriteError(err)
}

func (repository *authTransactionRepository) ConsumeExchange(
	ctx context.Context,
	codeHash []byte,
	now time.Time,
) (AuthExchange, error) {
	if len(codeHash) != 32 || now.IsZero() {
		return AuthExchange{}, errors.New("exchange code hash and current time are required")
	}
	query := repository.bind(`DELETE FROM auth_exchanges
		WHERE code_hash = ? AND expires_at > ?
		RETURNING schema_version, code_hash, principal_id, provider_id,
			client_id, redirect_uri, scope, nonce, pkce_challenge, created_at, expires_at`)
	exchange, err := scanAuthExchange(repository.executor.QueryRowContext(ctx, query, codeHash, formatTime(now)))
	if errors.Is(err, sql.ErrNoRows) {
		return AuthExchange{}, ErrNotFound
	}
	if err != nil {
		return AuthExchange{}, databaseError("consume authentication exchange", err)
	}
	return exchange, nil
}

func (repository *authTransactionRepository) DeleteExpired(ctx context.Context, before time.Time, limit int) (int64, error) {
	limit, err := boundedLimit(limit)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, table := range []string{"auth_attempts", "auth_exchanges"} {
		query := `DELETE FROM ` + table + ` WHERE rowid IN (
			SELECT rowid FROM ` + table + ` WHERE expires_at < ? ORDER BY expires_at LIMIT ?
		)`
		if repository.backend == BackendPostgreSQL {
			query = `DELETE FROM ` + table + ` WHERE ctid IN (
				SELECT ctid FROM ` + table + ` WHERE expires_at < $1 ORDER BY expires_at LIMIT $2
			)`
		}
		result, err := repository.executor.ExecContext(ctx, query, formatTime(before), limit)
		if err != nil {
			return total, databaseError("delete expired authentication transactions", err)
		}
		count, err := rowsAffected(result)
		if err != nil {
			return total, err
		}
		total += count
	}
	return total, nil
}

func normalizeAuthAttempt(attempt *AuthAttempt) error {
	attempt.ProviderID = strings.TrimSpace(attempt.ProviderID)
	attempt.ClientState = strings.TrimSpace(attempt.ClientState)
	attempt.ClientCallback = strings.TrimSpace(attempt.ClientCallback)
	attempt.ClientID = strings.TrimSpace(attempt.ClientID)
	attempt.Scope = strings.TrimSpace(attempt.Scope)
	attempt.Nonce = strings.TrimSpace(attempt.Nonce)
	attempt.PKCEChallenge = strings.TrimSpace(attempt.PKCEChallenge)
	attempt.UpstreamPKCEVerifier = strings.TrimSpace(attempt.UpstreamPKCEVerifier)
	if err := validateUUID(attempt.ID, "authentication attempt ID"); err != nil {
		return err
	}
	if attempt.ProviderID == "" || len(attempt.StateHash) != 32 || attempt.ClientState == "" ||
		attempt.ClientCallback == "" || attempt.ClientID == "" || attempt.Scope == "" ||
		attempt.Nonce == "" || attempt.PKCEChallenge == "" {
		return errors.New("authentication attempt fields are required")
	}
	if attempt.UpstreamPKCEVerifier == "" {
		return errors.New("authentication attempt fields are required")
	}
	if attempt.SchemaVersion == 0 {
		attempt.SchemaVersion = ObjectSchemaVersion
	}
	if attempt.SchemaVersion != ObjectSchemaVersion || attempt.CreatedAt.IsZero() ||
		attempt.ExpiresAt.IsZero() || !attempt.ExpiresAt.After(attempt.CreatedAt) {
		return errors.New("invalid authentication attempt schema or expiry")
	}
	attempt.StateHash = append([]byte(nil), attempt.StateHash...)
	attempt.CreatedAt = attempt.CreatedAt.UTC()
	attempt.ExpiresAt = attempt.ExpiresAt.UTC()
	return nil
}

func normalizeAuthExchange(exchange *AuthExchange) error {
	exchange.PrincipalID = strings.TrimSpace(exchange.PrincipalID)
	exchange.ProviderID = strings.TrimSpace(exchange.ProviderID)
	exchange.ClientID = strings.TrimSpace(exchange.ClientID)
	exchange.RedirectURI = strings.TrimSpace(exchange.RedirectURI)
	exchange.Scope = strings.TrimSpace(exchange.Scope)
	exchange.Nonce = strings.TrimSpace(exchange.Nonce)
	exchange.PKCEChallenge = strings.TrimSpace(exchange.PKCEChallenge)
	if err := validateUUID(exchange.PrincipalID, "exchange principal ID"); err != nil {
		return err
	}
	if len(exchange.CodeHash) != 32 || exchange.ProviderID == "" || exchange.ClientID == "" ||
		exchange.RedirectURI == "" || exchange.Scope == "" || exchange.PKCEChallenge == "" {
		return errors.New("authentication exchange fields are required")
	}
	if exchange.SchemaVersion == 0 {
		exchange.SchemaVersion = ObjectSchemaVersion
	}
	if exchange.SchemaVersion != ObjectSchemaVersion || exchange.CreatedAt.IsZero() ||
		exchange.ExpiresAt.IsZero() || !exchange.ExpiresAt.After(exchange.CreatedAt) {
		return errors.New("invalid authentication exchange schema or expiry")
	}
	exchange.CodeHash = append([]byte(nil), exchange.CodeHash...)
	exchange.CreatedAt = exchange.CreatedAt.UTC()
	exchange.ExpiresAt = exchange.ExpiresAt.UTC()
	return nil
}

func scanAuthAttempt(row rowScanner) (AuthAttempt, error) {
	var attempt AuthAttempt
	var createdAt, expiresAt string
	if err := row.Scan(
		&attempt.ID, &attempt.SchemaVersion, &attempt.ProviderID, &attempt.StateHash,
		&attempt.ClientState, &attempt.ClientCallback, &attempt.ClientID, &attempt.Scope, &attempt.Nonce,
		&attempt.PKCEChallenge, &attempt.UpstreamPKCEVerifier, &createdAt, &expiresAt,
	); err != nil {
		return AuthAttempt{}, err
	}
	var err error
	if attempt.CreatedAt, err = parseTime(createdAt, "authentication attempt creation time"); err != nil {
		return AuthAttempt{}, err
	}
	if attempt.ExpiresAt, err = parseTime(expiresAt, "authentication attempt expiry"); err != nil {
		return AuthAttempt{}, err
	}
	return attempt, nil
}

func scanAuthExchange(row rowScanner) (AuthExchange, error) {
	var exchange AuthExchange
	var createdAt, expiresAt string
	if err := row.Scan(
		&exchange.SchemaVersion, &exchange.CodeHash, &exchange.PrincipalID,
		&exchange.ProviderID, &exchange.ClientID, &exchange.RedirectURI, &exchange.Scope,
		&exchange.Nonce, &exchange.PKCEChallenge, &createdAt, &expiresAt,
	); err != nil {
		return AuthExchange{}, err
	}
	var err error
	if exchange.CreatedAt, err = parseTime(createdAt, "authentication exchange creation time"); err != nil {
		return AuthExchange{}, err
	}
	if exchange.ExpiresAt, err = parseTime(expiresAt, "authentication exchange expiry"); err != nil {
		return AuthExchange{}, err
	}
	return exchange, nil
}
