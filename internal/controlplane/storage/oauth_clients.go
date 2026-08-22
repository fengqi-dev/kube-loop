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

func (repository *oauthClientRepository) Create(
	ctx context.Context,
	client OAuthClient,
) error {
	if err := normalizeOAuthClient(&client, true); err != nil {
		return err
	}
	redirects, _ := json.Marshal(client.RedirectURIs)
	grants, _ := json.Marshal(client.GrantTypes)
	scopes, _ := json.Marshal(client.Scopes)
	query := repository.bind(
		`INSERT INTO oauth_clients(id, name, public, redirect_uris_json,
		grant_types_json, scopes_json, trusted, enabled, builtin, machine_identity_id,
		created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
	)
	_, err := repository.executor.ExecContext(
		ctx,
		query,
		client.ID,
		client.Name,
		client.Public,
		string(redirects),
		string(grants),
		string(scopes),
		client.Trusted,
		client.Enabled,
		client.Builtin,
		nullableString(client.MachineIdentityID),
		formatTime(client.CreatedAt),
		formatTime(client.UpdatedAt),
	)
	return mapWriteError(err)
}

func (repository *oauthClientRepository) Get(
	ctx context.Context,
	id string,
) (OAuthClient, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return OAuthClient{}, errors.New("oAuth client ID is required")
	}
	query := repository.bind(
		`SELECT id, name, public, redirect_uris_json, grant_types_json,
		scopes_json, trusted, enabled, builtin, machine_identity_id, created_at, updated_at
		FROM oauth_clients WHERE id = ?`,
	)
	client, err := scanOAuthClient(
		repository.executor.QueryRowContext(ctx, query, id),
	)
	if errors.Is(err, sql.ErrNoRows) {
		return OAuthClient{}, ErrNotFound
	}
	if err != nil {
		return OAuthClient{}, databaseError("read OAuth client", err)
	}
	return client, nil
}

func (repository *oauthClientRepository) List(
	ctx context.Context,
) ([]OAuthClient, error) {
	rows, err := repository.executor.QueryContext(
		ctx,
		`SELECT id, name, public, redirect_uris_json,
		grant_types_json, scopes_json, trusted, enabled, builtin, machine_identity_id, created_at, updated_at
		FROM oauth_clients ORDER BY builtin DESC, name, id`,
	)
	if err != nil {
		return nil, databaseError("list OAuth clients", err)
	}
	defer func() { _ = rows.Close() }()
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

func (repository *oauthClientRepository) Update(
	ctx context.Context,
	client OAuthClient,
) error {
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
		client.GrantTypes = []string{grantAuthorizationCode, grantRefreshToken}
	}
	redirects, _ := json.Marshal(client.RedirectURIs)
	grants, _ := json.Marshal(client.GrantTypes)
	scopes, _ := json.Marshal(client.Scopes)
	query := repository.bind(
		`UPDATE oauth_clients SET name = ?, public = ?, redirect_uris_json = ?,
		grant_types_json = ?, scopes_json = ?, trusted = ?, enabled = ?,
		machine_identity_id = ?, updated_at = ? WHERE id = ?`,
	)
	result, err := repository.executor.ExecContext(
		ctx,
		query,
		client.Name,
		client.Public,
		string(
			redirects,
		),
		string(grants),
		string(scopes),
		client.Trusted,
		client.Enabled,
		nullableString(
			client.MachineIdentityID,
		),
		formatTime(client.UpdatedAt),
		client.ID,
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

func (repository *oauthClientRepository) Delete(
	ctx context.Context,
	id string,
) error {
	client, err := repository.Get(ctx, id)
	if err != nil {
		return err
	}
	if client.Builtin {
		return errors.New("built-in OAuth clients cannot be deleted")
	}
	result, err := repository.executor.ExecContext(
		ctx,
		repository.bind(`DELETE FROM oauth_clients WHERE id = ?`),
		id,
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
	if client.ID == "" || len(client.ID) > 128 || client.Name == "" ||
		len(client.Name) > 128 {
		return errors.New("oAuth client identity is invalid")
	}
	for _, values := range [][]string{client.GrantTypes, client.Scopes} {
		if len(values) == 0 {
			return errors.New("oAuth client capabilities must not be empty")
		}
		for _, value := range values {
			if strings.TrimSpace(value) == "" {
				return errors.New("oAuth client capability is invalid")
			}
		}
	}
	slices.Sort(client.RedirectURIs)
	client.RedirectURIs = slices.Compact(client.RedirectURIs)
	slices.Sort(client.GrantTypes)
	client.GrantTypes = slices.Compact(client.GrantTypes)
	for _, grant := range client.GrantTypes {
		if grant != grantAuthorizationCode && grant != grantRefreshToken &&
			grant != "client_credentials" {
			return errors.New("oAuth client grant type is not supported")
		}
	}
	if client.Public &&
		slices.Contains(client.GrantTypes, "client_credentials") {
		return errors.New("public OAuth clients cannot use client credentials")
	}
	if slices.Contains(client.GrantTypes, grantAuthorizationCode) &&
		len(client.RedirectURIs) == 0 {
		return errors.New(
			"authorization code OAuth clients require a redirect URI",
		)
	}
	if slices.Contains(client.GrantTypes, "client_credentials") {
		for _, scope := range []string{scopeOpenID, scopeProfile, emailField, scopeOfflineAccess} {
			if slices.Contains(client.Scopes, scope) {
				return errors.New(
					"client credentials OAuth clients cannot use identity scopes",
				)
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
