package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
