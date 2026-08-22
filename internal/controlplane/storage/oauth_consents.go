package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type oauthConsentRepository struct{ repositoryBase }

func (repository *oauthConsentRepository) Grant(
	ctx context.Context,
	consent OAuthConsent,
) error {
	if len(consent.ScopeHash) != 32 ||
		strings.TrimSpace(consent.IdentityID) == "" ||
		strings.TrimSpace(consent.ClientID) == "" {
		return errors.New("oAuth consent is invalid")
	}
	raw, err := json.Marshal(consent.Scopes)
	if err != nil {
		return errors.New("encode OAuth consent scopes")
	}
	if consent.CreatedAt.IsZero() {
		consent.CreatedAt = time.Now()
	}
	if consent.UpdatedAt.IsZero() {
		consent.UpdatedAt = consent.CreatedAt
	}
	query := `INSERT INTO oauth_consents(identity_id, client_id, scope_hash, scopes_json, created_at, updated_at)` +
		` VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(identity_id, client_id, scope_hash)` +
		` DO UPDATE SET scopes_json=excluded.scopes_json, updated_at=excluded.updated_at`
	if repository.backend == BackendPostgreSQL {
		query = `INSERT INTO oauth_consents(identity_id, client_id, scope_hash, scopes_json, created_at, updated_at)` +
			` VALUES ($1, $2, $3, $4::jsonb, $5, $6) ON CONFLICT(identity_id, client_id, scope_hash)` +
			` DO UPDATE SET scopes_json=excluded.scopes_json, updated_at=excluded.updated_at`
	}
	if repository.backend == BackendMySQL {
		query = `INSERT INTO oauth_consents(identity_id, client_id, scope_hash, scopes_json, created_at, updated_at)` +
			` VALUES (?, ?, ?, ?, ?, ?)` +
			` ON DUPLICATE KEY UPDATE scopes_json=VALUES(scopes_json), updated_at=VALUES(updated_at)`
	}
	_, err = repository.executor.ExecContext(
		ctx,
		query,
		consent.IdentityID,
		consent.ClientID,
		consent.ScopeHash,
		string(raw),
		formatTime(consent.CreatedAt),
		formatTime(consent.UpdatedAt),
	)
	return mapWriteError(err)
}

func (repository *oauthConsentRepository) Has(
	ctx context.Context,
	identityID, clientID string,
	scopeHash []byte,
) (bool, error) {
	var one int
	query := repository.bind(
		`SELECT 1 FROM oauth_consents WHERE identity_id = ? AND client_id = ? AND scope_hash = ?`,
	)
	err := repository.executor.QueryRowContext(ctx, query, identityID, clientID, scopeHash).
		Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, databaseError("read OAuth consent", err)
	}
	return true, nil
}

func (repository *oauthConsentRepository) RevokeClient(
	ctx context.Context,
	identityID, clientID string,
) error {
	_, err := repository.executor.ExecContext(
		ctx,
		repository.bind(
			`DELETE FROM oauth_consents WHERE identity_id = ? AND client_id = ?`,
		),
		identityID,
		clientID,
	)
	return mapWriteError(err)
}
