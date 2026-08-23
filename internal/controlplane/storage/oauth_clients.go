package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
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
