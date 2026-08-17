package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
)

type oauthClientRepository struct{ repositoryBase }
type oauthSessionRepository struct{ repositoryBase }
type oauthConsentRepository struct{ repositoryBase }

func (repository *oauthClientRepository) Create(ctx context.Context, client OAuthClient) error {
	if err := normalizeOAuthClient(&client, true); err != nil {
		return err
	}
	redirects, _ := json.Marshal(client.RedirectURIs)
	grants, _ := json.Marshal(client.GrantTypes)
	scopes, _ := json.Marshal(client.Scopes)
	query := repository.bind(`INSERT INTO oauth_clients(id, name, public, redirect_uris_json,
		grant_types_json, scopes_json, trusted, enabled, builtin, machine_identity_id,
		created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err := repository.executor.ExecContext(ctx, query, client.ID, client.Name,
		client.Public, string(redirects), string(grants), string(scopes), client.Trusted,
		client.Enabled, client.Builtin, nullableString(client.MachineIdentityID), formatTime(client.CreatedAt), formatTime(client.UpdatedAt))
	return mapWriteError(err)
}

func (repository *oauthClientRepository) Get(ctx context.Context, id string) (OAuthClient, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return OAuthClient{}, errors.New("OAuth client ID is required")
	}
	query := repository.bind(`SELECT id, name, public, redirect_uris_json, grant_types_json,
		scopes_json, trusted, enabled, builtin, machine_identity_id, created_at, updated_at
		FROM oauth_clients WHERE id = ?`)
	client, err := scanOAuthClient(repository.executor.QueryRowContext(ctx, query, id))
	if errors.Is(err, sql.ErrNoRows) {
		return OAuthClient{}, ErrNotFound
	}
	if err != nil {
		return OAuthClient{}, databaseError("read OAuth client", err)
	}
	return client, nil
}

func (repository *oauthClientRepository) List(ctx context.Context) ([]OAuthClient, error) {
	rows, err := repository.executor.QueryContext(ctx, `SELECT id, name, public, redirect_uris_json,
		grant_types_json, scopes_json, trusted, enabled, builtin, machine_identity_id, created_at, updated_at
		FROM oauth_clients ORDER BY builtin DESC, name, id`)
	if err != nil {
		return nil, databaseError("list OAuth clients", err)
	}
	defer rows.Close()
	clients := make([]OAuthClient, 0)
	for rows.Next() {
		client, scanErr := scanOAuthClient(rows)
		if scanErr != nil {
			return nil, databaseError("decode OAuth client", scanErr)
		}
		clients = append(clients, client)
	}
	if err := rows.Err(); err != nil {
		return nil, databaseError("iterate OAuth clients", err)
	}
	return clients, nil
}

func (repository *oauthClientRepository) Update(ctx context.Context, client OAuthClient) error {
	if err := normalizeOAuthClient(&client, false); err != nil {
		return err
	}
	existing, err := repository.Get(ctx, client.ID)
	if err != nil {
		return err
	}
	if existing.Builtin {
		client.Builtin = true
		client.Public = true
		client.Trusted = true
		client.Enabled = true
		client.GrantTypes = []string{"authorization_code", "refresh_token"}
	}
	redirects, _ := json.Marshal(client.RedirectURIs)
	grants, _ := json.Marshal(client.GrantTypes)
	scopes, _ := json.Marshal(client.Scopes)
	query := repository.bind(`UPDATE oauth_clients SET name = ?, public = ?, redirect_uris_json = ?,
		grant_types_json = ?, scopes_json = ?, trusted = ?, enabled = ?,
		machine_identity_id = ?, updated_at = ? WHERE id = ?`)
	result, err := repository.executor.ExecContext(ctx, query, client.Name, client.Public,
		string(redirects), string(grants), string(scopes), client.Trusted, client.Enabled,
		nullableString(client.MachineIdentityID), formatTime(client.UpdatedAt), client.ID)
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

func (repository *oauthClientRepository) Delete(ctx context.Context, id string) error {
	client, err := repository.Get(ctx, id)
	if err != nil {
		return err
	}
	if client.Builtin {
		return errors.New("built-in OAuth clients cannot be deleted")
	}
	result, err := repository.executor.ExecContext(ctx, repository.bind(`DELETE FROM oauth_clients WHERE id = ?`), id)
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

func (repository *oauthClientRepository) SetSecret(ctx context.Context, secret OAuthClientSecret) error {
	if strings.TrimSpace(secret.ClientID) == "" || len(secret.SecretHash) < 32 {
		return errors.New("OAuth client secret hash is invalid")
	}
	if secret.CreatedAt.IsZero() {
		secret.CreatedAt = time.Now()
	}
	if secret.UpdatedAt.IsZero() {
		secret.UpdatedAt = secret.CreatedAt
	}
	query := `INSERT INTO oauth_client_secrets(client_id, secret_hash, created_at, updated_at)
		VALUES (?, ?, ?, ?) ON CONFLICT(client_id) DO UPDATE SET secret_hash=excluded.secret_hash, updated_at=excluded.updated_at`
	if repository.backend == BackendPostgreSQL {
		query = `INSERT INTO oauth_client_secrets(client_id, secret_hash, created_at, updated_at)
		VALUES ($1, $2, $3, $4) ON CONFLICT(client_id) DO UPDATE SET secret_hash=excluded.secret_hash, updated_at=excluded.updated_at`
	}
	if repository.backend == BackendMySQL {
		query = `INSERT INTO oauth_client_secrets(client_id, secret_hash, created_at, updated_at)
		VALUES (?, ?, ?, ?) ON DUPLICATE KEY UPDATE secret_hash=VALUES(secret_hash), updated_at=VALUES(updated_at)`
	}
	_, err := repository.executor.ExecContext(ctx, query, secret.ClientID, secret.SecretHash, formatTime(secret.CreatedAt), formatTime(secret.UpdatedAt))
	return mapWriteError(err)
}

func (repository *oauthClientRepository) GetSecret(ctx context.Context, clientID string) (OAuthClientSecret, error) {
	var value OAuthClientSecret
	var created, updated string
	err := repository.executor.QueryRowContext(ctx, repository.bind(`SELECT client_id, secret_hash, created_at, updated_at FROM oauth_client_secrets WHERE client_id = ?`), clientID).Scan(&value.ClientID, &value.SecretHash, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return OAuthClientSecret{}, ErrNotFound
	}
	if err != nil {
		return OAuthClientSecret{}, databaseError("read OAuth client secret", err)
	}
	value.CreatedAt, err = parseTime(created, "OAuth client secret creation time")
	if err == nil {
		value.UpdatedAt, err = parseTime(updated, "OAuth client secret update time")
	}
	return value, err
}

func scanOAuthClient(row rowScanner) (OAuthClient, error) {
	var c OAuthClient
	var redirects, grants, scopes, created, updated string
	var machine sql.NullString
	err := row.Scan(&c.ID, &c.Name, &c.Public, &redirects, &grants, &scopes,
		&c.Trusted, &c.Enabled, &c.Builtin, &machine, &created, &updated)
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal([]byte(redirects), &c.RedirectURIs); err != nil {
		return c, errors.New("decode OAuth client redirect URIs")
	}
	if err = json.Unmarshal([]byte(grants), &c.GrantTypes); err != nil {
		return c, errors.New("decode OAuth client grant types")
	}
	if err = json.Unmarshal([]byte(scopes), &c.Scopes); err != nil {
		return c, errors.New("decode OAuth client scopes")
	}
	c.MachineIdentityID = machine.String
	c.CreatedAt, err = parseTime(created, "OAuth client creation time")
	if err == nil {
		c.UpdatedAt, err = parseTime(updated, "OAuth client update time")
	}
	return c, err
}

func normalizeOAuthClient(client *OAuthClient, create bool) error {
	client.ID = strings.TrimSpace(client.ID)
	client.Name = strings.TrimSpace(client.Name)
	if client.ID == "" || len(client.ID) > 128 || client.Name == "" || len(client.Name) > 128 {
		return errors.New("OAuth client identity is invalid")
	}
	for _, values := range [][]string{client.GrantTypes, client.Scopes} {
		if len(values) == 0 {
			return errors.New("OAuth client capabilities must not be empty")
		}
		for _, value := range values {
			if strings.TrimSpace(value) == "" {
				return errors.New("OAuth client capability is invalid")
			}
		}
	}
	slices.Sort(client.RedirectURIs)
	client.RedirectURIs = slices.Compact(client.RedirectURIs)
	slices.Sort(client.GrantTypes)
	client.GrantTypes = slices.Compact(client.GrantTypes)
	for _, grant := range client.GrantTypes {
		if grant != "authorization_code" && grant != "refresh_token" && grant != "client_credentials" {
			return errors.New("OAuth client grant type is not supported")
		}
	}
	if client.Public && slices.Contains(client.GrantTypes, "client_credentials") {
		return errors.New("public OAuth clients cannot use client credentials")
	}
	if slices.Contains(client.GrantTypes, "authorization_code") && len(client.RedirectURIs) == 0 {
		return errors.New("authorization code OAuth clients require a redirect URI")
	}
	if slices.Contains(client.GrantTypes, "client_credentials") {
		for _, scope := range []string{"openid", "profile", "email", "offline_access"} {
			if slices.Contains(client.Scopes, scope) {
				return errors.New("client credentials OAuth clients cannot use identity scopes")
			}
		}
	}
	slices.Sort(client.Scopes)
	client.Scopes = slices.Compact(client.Scopes)
	now := time.Now().UTC()
	if create && client.CreatedAt.IsZero() {
		client.CreatedAt = now
	}
	if client.UpdatedAt.IsZero() {
		client.UpdatedAt = now
	}
	if client.CreatedAt.IsZero() {
		client.CreatedAt = client.UpdatedAt
	}
	return nil
}

func (repository *oauthSessionRepository) Create(ctx context.Context, session OAuthSession) error {
	if len(session.SignatureHash) != 32 || strings.TrimSpace(session.Kind) == "" || strings.TrimSpace(session.RequestID) == "" || !json.Valid(session.RequestJSON) {
		return errors.New("OAuth session is invalid")
	}
	if session.Status == "" {
		session.Status = "active"
	}
	if session.CreatedAt.IsZero() {
		session.CreatedAt = time.Now()
	}
	if !session.ExpiresAt.After(session.CreatedAt) {
		return errors.New("OAuth session expiry is invalid")
	}
	_, err := repository.executor.ExecContext(ctx, repository.bind(`INSERT INTO oauth_sessions(kind, signature_hash, request_id,
		identity_id, client_id, device_id, request_json, status, created_at, expires_at, revoked_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`), session.Kind, session.SignatureHash, session.RequestID,
		nullableString(session.IdentityID), session.ClientID, session.DeviceID, string(session.RequestJSON), session.Status,
		formatTime(session.CreatedAt), formatTime(session.ExpiresAt), nullableTime(session.RevokedAt))
	return mapWriteError(err)
}

func (repository *oauthSessionRepository) ListGrants(ctx context.Context, filter OAuthGrantListFilter) ([]OAuthGrant, error) {
	limit, cursor, err := normalizePage(filter.Limit, filter.Cursor)
	if err != nil {
		return nil, err
	}
	filter.IdentityID = strings.TrimSpace(filter.IdentityID)
	filter.ClientID = strings.TrimSpace(filter.ClientID)
	filter.Status = strings.TrimSpace(filter.Status)
	if filter.IdentityID != "" && validateUUID(filter.IdentityID, "OAuth grant identity ID") != nil {
		return nil, errors.New("OAuth grant identity filter is invalid")
	}
	if len(filter.ClientID) > 128 || strings.ContainsAny(filter.ClientID, "\x00\r\n") {
		return nil, errors.New("OAuth grant client filter is invalid")
	}
	if filter.Status != "" && filter.Status != "active" && filter.Status != "revoked" && filter.Status != "expired" {
		return nil, errors.New("OAuth grant status filter is invalid")
	}
	if filter.Now.IsZero() {
		filter.Now = time.Now().UTC()
	}
	query := `WITH grants AS (
		SELECT request_id AS id, MAX(identity_id) AS identity_id, MAX(client_id) AS client_id,
			MAX(device_id) AS device_id, MIN(created_at) AS created_at, MAX(expires_at) AS expires_at,
			MAX(revoked_at) AS revoked_at,
			CASE
				WHEN SUM(CASE WHEN status = 'active' AND expires_at > ? THEN 1 ELSE 0 END) > 0 THEN 'active'
				WHEN SUM(CASE WHEN status = 'revoked' OR revoked_at IS NOT NULL THEN 1 ELSE 0 END) > 0 THEN 'revoked'
				ELSE 'expired'
			END AS status
		FROM oauth_sessions
		WHERE kind IN ('access_token', 'refresh_token')
		GROUP BY request_id
	)
	SELECT id, identity_id, client_id, device_id,
		(SELECT request_json FROM oauth_sessions AS source WHERE source.request_id = grants.id
		 ORDER BY CASE WHEN source.kind = 'refresh_token' THEN 0 ELSE 1 END LIMIT 1),
		status, created_at, expires_at, revoked_at
	FROM grants WHERE 1=1`
	arguments := []any{formatTime(filter.Now)}
	if filter.IdentityID != "" {
		query += ` AND identity_id = ?`
		arguments = append(arguments, filter.IdentityID)
	}
	if filter.ClientID != "" {
		query += ` AND client_id = ?`
		arguments = append(arguments, filter.ClientID)
	}
	if filter.Status != "" {
		query += ` AND status = ?`
		arguments = append(arguments, filter.Status)
	}
	query, arguments = appendPageBoundary(query, arguments, "", cursor)
	query += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	arguments = append(arguments, limit)
	rows, err := repository.executor.QueryContext(ctx, repository.bind(query), arguments...)
	if err != nil {
		return nil, databaseError("list OAuth grants", err)
	}
	defer rows.Close()
	grants := make([]OAuthGrant, 0)
	for rows.Next() {
		var grant OAuthGrant
		var raw, created, expires string
		var identity, revoked sql.NullString
		if err := rows.Scan(&grant.RequestID, &identity, &grant.ClientID, &grant.DeviceID, &raw,
			&grant.Status, &created, &expires, &revoked); err != nil {
			return nil, databaseError("decode OAuth grant", err)
		}
		grant.IdentityID = identity.String
		grant.CreatedAt, err = parseTime(created, "OAuth grant creation time")
		if err == nil {
			grant.ExpiresAt, err = parseTime(expires, "OAuth grant expiry")
		}
		if err == nil {
			grant.RevokedAt, err = parseNullableTime(revoked, "OAuth grant revocation time")
		}
		if err == nil {
			grant.Scopes, err = oauthGrantScopes([]byte(raw))
		}
		if err != nil {
			return nil, err
		}
		grants = append(grants, grant)
	}
	if err := rows.Err(); err != nil {
		return nil, databaseError("iterate OAuth grants", err)
	}
	return grants, nil
}

func oauthGrantScopes(raw []byte) ([]string, error) {
	var document struct {
		Requested []string `json:"requested_scopes"`
		Granted   []string `json:"granted_scopes"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, errors.New("decode OAuth grant scopes")
	}
	result := document.Granted
	if len(result) == 0 {
		result = document.Requested
	}
	result = append([]string(nil), result...)
	slices.Sort(result)
	return slices.Compact(result), nil
}

func (repository *oauthSessionRepository) Get(ctx context.Context, kind string, hash []byte) (OAuthSession, error) {
	return repository.get(ctx, kind, hash)
}
func (repository *oauthSessionRepository) get(ctx context.Context, kind string, hash []byte) (OAuthSession, error) {
	var value OAuthSession
	var raw, created, expires string
	var revoked sql.NullString
	var identity sql.NullString
	err := repository.executor.QueryRowContext(ctx, repository.bind(`SELECT kind, signature_hash, request_id, identity_id,
		client_id, device_id, request_json, status, created_at, expires_at, revoked_at FROM oauth_sessions
		WHERE kind = ? AND signature_hash = ?`), kind, hash).Scan(&value.Kind, &value.SignatureHash, &value.RequestID,
		&identity, &value.ClientID, &value.DeviceID, &raw, &value.Status, &created, &expires, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return OAuthSession{}, ErrNotFound
	}
	if err != nil {
		return OAuthSession{}, databaseError("read OAuth session", err)
	}
	value.RequestJSON = json.RawMessage(raw)
	value.IdentityID = identity.String
	value.CreatedAt, err = parseTime(created, "OAuth session creation time")
	if err == nil {
		value.ExpiresAt, err = parseTime(expires, "OAuth session expiry")
	}
	if err == nil {
		value.RevokedAt, err = parseNullableTime(revoked, "OAuth session revocation time")
	}
	return value, err
}

func (repository *oauthSessionRepository) Consume(ctx context.Context, kind string, hash []byte, now time.Time) (OAuthSession, error) {
	result, err := repository.executor.ExecContext(ctx, repository.bind(`UPDATE oauth_sessions SET status = 'consumed' WHERE kind = ? AND signature_hash = ? AND status = 'active' AND expires_at > ?`), kind, hash, formatTime(now))
	if err != nil {
		return OAuthSession{}, mapWriteError(err)
	}
	count, err := rowsAffected(result)
	if err != nil {
		return OAuthSession{}, err
	}
	if count != 1 {
		return OAuthSession{}, ErrNotFound
	}
	return repository.get(ctx, kind, hash)
}

func (repository *oauthSessionRepository) Delete(ctx context.Context, kind string, hash []byte) error {
	_, err := repository.executor.ExecContext(ctx, repository.bind(`DELETE FROM oauth_sessions WHERE kind = ? AND signature_hash = ?`), kind, hash)
	return mapWriteError(err)
}

func (repository *oauthSessionRepository) RevokeRequest(ctx context.Context, requestID string, now time.Time) error {
	_, err := repository.executor.ExecContext(ctx, repository.bind(`UPDATE oauth_sessions SET status = 'revoked', revoked_at = ? WHERE request_id = ? AND status = 'active'`), formatTime(now), requestID)
	if err != nil {
		return mapWriteError(err)
	}
	_, err = repository.executor.ExecContext(ctx, repository.bind(`UPDATE admin_sessions SET revoked_at = COALESCE(revoked_at, ?) WHERE authorization_id = ?`), formatTime(now), requestID)
	return mapWriteError(err)
}

func (repository *oauthSessionRepository) RevokeIdentity(ctx context.Context, identityID string, now time.Time) (int64, error) {
	if strings.TrimSpace(identityID) == "" || now.IsZero() {
		return 0, errors.New("OAuth identity revocation is invalid")
	}
	result, err := repository.executor.ExecContext(ctx, repository.bind(`UPDATE oauth_sessions SET status = 'revoked', revoked_at = ?
		WHERE identity_id = ? AND status = 'active'`), formatTime(now), identityID)
	if err != nil {
		return 0, mapWriteError(err)
	}
	return rowsAffected(result)
}

func (repository *oauthSessionRepository) RequestOwner(ctx context.Context, requestID string) (string, string, error) {
	var identityID, deviceID string
	err := repository.executor.QueryRowContext(ctx, repository.bind(`SELECT identity_id, device_id FROM oauth_sessions
		WHERE request_id = ? AND identity_id IS NOT NULL ORDER BY CASE WHEN kind = 'refresh_token' THEN 0 ELSE 1 END LIMIT 1`), requestID).Scan(&identityID, &deviceID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", databaseError("read OAuth grant owner", err)
	}
	return identityID, deviceID, nil
}

func (repository *oauthSessionRepository) RequestActive(ctx context.Context, requestID string, now time.Time) (bool, error) {
	var count int
	err := repository.executor.QueryRowContext(ctx, repository.bind(`SELECT COUNT(*) FROM oauth_sessions WHERE request_id = ? AND status = 'active' AND expires_at > ? AND kind IN ('access_token', 'refresh_token')`), requestID, formatTime(now)).Scan(&count)
	if err != nil {
		return false, databaseError("read OAuth grant state", err)
	}
	return count > 0, nil
}
func (repository *oauthSessionRepository) DeleteExpired(ctx context.Context, now time.Time, limit int) (int64, error) {
	if _, err := boundedLimit(limit); err != nil {
		return 0, err
	}
	result, err := repository.executor.ExecContext(ctx, repository.bind(`DELETE FROM oauth_sessions WHERE expires_at <= ?`), formatTime(now))
	if err != nil {
		return 0, mapWriteError(err)
	}
	return rowsAffected(result)
}

func (repository *oauthConsentRepository) Grant(ctx context.Context, consent OAuthConsent) error {
	if len(consent.ScopeHash) != 32 || strings.TrimSpace(consent.IdentityID) == "" || strings.TrimSpace(consent.ClientID) == "" {
		return errors.New("OAuth consent is invalid")
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
	query := `INSERT INTO oauth_consents(identity_id, client_id, scope_hash, scopes_json, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(identity_id, client_id, scope_hash) DO UPDATE SET scopes_json=excluded.scopes_json, updated_at=excluded.updated_at`
	if repository.backend == BackendPostgreSQL {
		query = `INSERT INTO oauth_consents(identity_id, client_id, scope_hash, scopes_json, created_at, updated_at) VALUES ($1, $2, $3, $4::jsonb, $5, $6) ON CONFLICT(identity_id, client_id, scope_hash) DO UPDATE SET scopes_json=excluded.scopes_json, updated_at=excluded.updated_at`
	}
	if repository.backend == BackendMySQL {
		query = `INSERT INTO oauth_consents(identity_id, client_id, scope_hash, scopes_json, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?) ON DUPLICATE KEY UPDATE scopes_json=VALUES(scopes_json), updated_at=VALUES(updated_at)`
	}
	_, err = repository.executor.ExecContext(ctx, query, consent.IdentityID, consent.ClientID, consent.ScopeHash, string(raw), formatTime(consent.CreatedAt), formatTime(consent.UpdatedAt))
	return mapWriteError(err)
}
func (repository *oauthConsentRepository) Has(ctx context.Context, identityID, clientID string, scopeHash []byte) (bool, error) {
	var one int
	err := repository.executor.QueryRowContext(ctx, repository.bind(`SELECT 1 FROM oauth_consents WHERE identity_id = ? AND client_id = ? AND scope_hash = ?`), identityID, clientID, scopeHash).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, databaseError("read OAuth consent", err)
	}
	return true, nil
}
func (repository *oauthConsentRepository) RevokeClient(ctx context.Context, identityID, clientID string) error {
	_, err := repository.executor.ExecContext(ctx, repository.bind(`DELETE FROM oauth_consents WHERE identity_id = ? AND client_id = ?`), identityID, clientID)
	return mapWriteError(err)
}
