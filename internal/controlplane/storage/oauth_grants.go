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
