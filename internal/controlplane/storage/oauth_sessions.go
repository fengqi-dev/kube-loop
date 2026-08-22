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

type oauthSessionRepository struct{ repositoryBase }

func (repository *oauthSessionRepository) Create(
	ctx context.Context,
	session OAuthSession,
) error {
	if len(session.SignatureHash) != 32 ||
		strings.TrimSpace(session.Kind) == "" ||
		strings.TrimSpace(session.RequestID) == "" ||
		!json.Valid(session.RequestJSON) {
		return errors.New("oAuth session is invalid")
	}
	if session.Status == "" {
		session.Status = statusActive
	}
	if session.CreatedAt.IsZero() {
		session.CreatedAt = time.Now()
	}
	if !session.ExpiresAt.After(session.CreatedAt) {
		return errors.New("oAuth session expiry is invalid")
	}
	_, err := repository.executor.ExecContext(
		ctx,
		repository.bind(
			`INSERT INTO oauth_sessions(kind, signature_hash, request_id,
		identity_id, client_id, device_id, request_json, status, created_at, expires_at, revoked_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		),
		session.Kind,
		session.SignatureHash,
		session.RequestID,
		nullableString(
			session.IdentityID,
		),
		session.ClientID,
		session.DeviceID,
		string(session.RequestJSON),
		session.Status,
		formatTime(session.CreatedAt),
		formatTime(session.ExpiresAt),
		nullableTime(session.RevokedAt),
	)
	return mapWriteError(err)
}

func (repository *oauthSessionRepository) ListGrants(
	ctx context.Context,
	filter OAuthGrantListFilter,
) ([]OAuthGrant, error) {
	limit, cursor, err := normalizePage(filter.Limit, filter.Cursor)
	if err != nil {
		return nil, err
	}
	filter.IdentityID = strings.TrimSpace(filter.IdentityID)
	filter.ClientID = strings.TrimSpace(filter.ClientID)
	filter.Status = strings.TrimSpace(filter.Status)
	if filter.IdentityID != "" &&
		validateUUID(filter.IdentityID, "OAuth grant identity ID") != nil {
		return nil, errors.New("oAuth grant identity filter is invalid")
	}
	if len(filter.ClientID) > 128 ||
		strings.ContainsAny(filter.ClientID, "\x00\r\n") {
		return nil, errors.New("oAuth grant client filter is invalid")
	}
	if filter.Status != "" && filter.Status != statusActive &&
		filter.Status != "revoked" &&
		filter.Status != statusExpired {
		return nil, errors.New("oAuth grant status filter is invalid")
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
		query += identityFilterSQL
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
	query += descendingPageSQL
	arguments = append(arguments, limit)
	rows, err := repository.executor.QueryContext(
		ctx,
		repository.bind(query),
		arguments...)
	if err != nil {
		return nil, databaseError("list OAuth grants", err)
	}
	defer func() { _ = rows.Close() }()
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
			grant.RevokedAt, err = parseNullableTime(
				revoked,
				"OAuth grant revocation time",
			)
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

func (repository *oauthSessionRepository) Get(
	ctx context.Context,
	kind string,
	hash []byte,
) (OAuthSession, error) {
	return repository.get(ctx, kind, hash)
}

func (repository *oauthSessionRepository) get(
	ctx context.Context,
	kind string,
	hash []byte,
) (OAuthSession, error) {
	var value OAuthSession
	var raw, created, expires string
	var revoked sql.NullString
	var identity sql.NullString
	err := repository.executor.QueryRowContext(ctx, repository.bind(`SELECT kind, signature_hash, request_id, identity_id,
		client_id, device_id, request_json, status, created_at, expires_at, revoked_at FROM oauth_sessions
		WHERE kind = ? AND signature_hash = ?`), kind, hash).
		Scan(&value.Kind, &value.SignatureHash, &value.RequestID,
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
		value.RevokedAt, err = parseNullableTime(
			revoked,
			"OAuth session revocation time",
		)
	}
	return value, err
}

func (repository *oauthSessionRepository) Consume(
	ctx context.Context,
	kind string,
	hash []byte,
	now time.Time,
) (OAuthSession, error) {
	result, err := repository.executor.ExecContext(
		ctx,
		repository.bind(
			`UPDATE oauth_sessions SET status = 'consumed' WHERE kind = ? AND signature_hash = ?`+
				` AND status = 'active' AND expires_at > ?`,
		),
		kind,
		hash,
		formatTime(now),
	)
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

func (repository *oauthSessionRepository) Delete(
	ctx context.Context,
	kind string,
	hash []byte,
) error {
	_, err := repository.executor.ExecContext(
		ctx,
		repository.bind(
			`DELETE FROM oauth_sessions WHERE kind = ? AND signature_hash = ?`,
		),
		kind,
		hash,
	)
	return mapWriteError(err)
}

func (repository *oauthSessionRepository) RevokeRequest(
	ctx context.Context,
	requestID string,
	now time.Time,
) error {
	_, err := repository.executor.ExecContext(
		ctx,
		repository.bind(
			`UPDATE oauth_sessions SET status = 'revoked', revoked_at = ? WHERE request_id = ? AND status = 'active'`,
		),
		formatTime(now),
		requestID,
	)
	if err != nil {
		return mapWriteError(err)
	}
	_, err = repository.executor.ExecContext(
		ctx,
		repository.bind(
			`UPDATE admin_sessions SET revoked_at = COALESCE(revoked_at, ?) WHERE authorization_id = ?`,
		),
		formatTime(now),
		requestID,
	)
	return mapWriteError(err)
}

func (repository *oauthSessionRepository) RevokeIdentity(
	ctx context.Context,
	identityID string,
	now time.Time,
) (int64, error) {
	if strings.TrimSpace(identityID) == "" || now.IsZero() {
		return 0, errors.New("oAuth identity revocation is invalid")
	}
	result, err := repository.executor.ExecContext(
		ctx,
		repository.bind(
			`UPDATE oauth_sessions SET status = 'revoked', revoked_at = ?
		WHERE identity_id = ? AND status = 'active'`,
		),
		formatTime(now),
		identityID,
	)
	if err != nil {
		return 0, mapWriteError(err)
	}
	return rowsAffected(result)
}

func (repository *oauthSessionRepository) RequestOwner(
	ctx context.Context,
	requestID string,
) (string, string, error) {
	var identityID, deviceID string
	query := repository.bind(`SELECT identity_id, device_id FROM oauth_sessions
		WHERE request_id = ? AND identity_id IS NOT NULL` +
		` ORDER BY CASE WHEN kind = 'refresh_token' THEN 0 ELSE 1 END LIMIT 1`)
	err := repository.executor.QueryRowContext(ctx, query, requestID).
		Scan(&identityID, &deviceID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", databaseError("read OAuth grant owner", err)
	}
	return identityID, deviceID, nil
}

func (repository *oauthSessionRepository) RequestActive(
	ctx context.Context,
	requestID string,
	now time.Time,
) (bool, error) {
	var count int
	query := repository.bind(
		`SELECT COUNT(*) FROM oauth_sessions WHERE request_id = ? AND status = 'active'` +
			` AND expires_at > ? AND kind IN ('access_token', 'refresh_token')`,
	)
	err := repository.executor.QueryRowContext(ctx, query, requestID, formatTime(now)).
		Scan(&count)
	if err != nil {
		return false, databaseError("read OAuth grant state", err)
	}
	return count > 0, nil
}

func (repository *oauthSessionRepository) DeleteExpired(
	ctx context.Context,
	now time.Time,
	limit int,
) (int64, error) {
	if _, err := boundedLimit(limit); err != nil {
		return 0, err
	}
	result, err := repository.executor.ExecContext(
		ctx,
		repository.bind(`DELETE FROM oauth_sessions WHERE expires_at <= ?`),
		formatTime(now),
	)
	if err != nil {
		return 0, mapWriteError(err)
	}
	return rowsAffected(result)
}
