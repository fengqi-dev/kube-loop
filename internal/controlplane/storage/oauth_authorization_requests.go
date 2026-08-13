package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type oauthAuthorizationRequestRepository struct{ repositoryBase }

func (repository *oauthAuthorizationRequestRepository) Create(ctx context.Context, request OAuthAuthorizationRequest) error {
	if len(request.ChallengeHash) != 32 || len(request.CSRFHash) != 32 || request.RequestID == "" || !json.Valid(request.RequestJSON) {
		return errors.New("OAuth authorization request is invalid")
	}
	if request.Status == "" {
		request.Status = "pending"
	}
	if request.CreatedAt.IsZero() {
		request.CreatedAt = time.Now().UTC()
	}
	if !request.ExpiresAt.After(request.CreatedAt) {
		return errors.New("OAuth authorization request expiry is invalid")
	}
	_, err := repository.executor.ExecContext(ctx, repository.bind(`INSERT INTO oauth_authorization_requests(
		challenge_hash, upstream_state_hash, request_id, request_json, csrf_hash, principal_id, provider_id, status, created_at, expires_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`), request.ChallengeHash, nullableBytes(request.UpstreamStateHash), request.RequestID, string(request.RequestJSON),
		request.CSRFHash, nullableString(request.PrincipalID), request.ProviderID, request.Status,
		formatTime(request.CreatedAt), formatTime(request.ExpiresAt))
	return mapWriteError(err)
}

func (repository *oauthAuthorizationRequestRepository) SetUpstream(ctx context.Context, challengeHash, stateHash []byte, raw json.RawMessage, providerID string, now time.Time) error {
	if len(challengeHash) != 32 || len(stateHash) != 32 || !json.Valid(raw) || providerID == "" || now.IsZero() {
		return errors.New("OAuth upstream authorization request is invalid")
	}
	result, err := repository.executor.ExecContext(ctx, repository.bind(`UPDATE oauth_authorization_requests
		SET upstream_state_hash = ?, request_json = ?, provider_id = ?
		WHERE challenge_hash = ? AND status = 'pending' AND expires_at > ?`),
		stateHash, string(raw), providerID, challengeHash, formatTime(now))
	if err != nil {
		return mapWriteError(err)
	}
	count, err := rowsAffected(result)
	if err == nil && count != 1 {
		return ErrNotFound
	}
	return err
}

func (repository *oauthAuthorizationRequestRepository) ConsumeUpstream(ctx context.Context, stateHash []byte, now time.Time) (OAuthAuthorizationRequest, error) {
	if len(stateHash) != 32 || now.IsZero() {
		return OAuthAuthorizationRequest{}, ErrNotFound
	}
	var challengeHash []byte
	err := repository.executor.QueryRowContext(ctx, repository.bind(`SELECT challenge_hash FROM oauth_authorization_requests
		WHERE upstream_state_hash = ? AND status = 'pending' AND expires_at > ?`), stateHash, formatTime(now)).Scan(&challengeHash)
	if errors.Is(err, sql.ErrNoRows) {
		return OAuthAuthorizationRequest{}, ErrNotFound
	}
	if err != nil {
		return OAuthAuthorizationRequest{}, databaseError("read OAuth upstream authorization request", err)
	}
	result, err := repository.executor.ExecContext(ctx, repository.bind(`UPDATE oauth_authorization_requests
		SET upstream_state_hash = NULL WHERE challenge_hash = ? AND upstream_state_hash = ? AND status = 'pending'`), challengeHash, stateHash)
	if err != nil {
		return OAuthAuthorizationRequest{}, mapWriteError(err)
	}
	count, err := rowsAffected(result)
	if err != nil || count != 1 {
		return OAuthAuthorizationRequest{}, ErrNotFound
	}
	return repository.get(ctx, challengeHash, now, false)
}

func (repository *oauthAuthorizationRequestRepository) Continue(ctx context.Context, oldHash, nextHash, csrfHash []byte, principalID string, now time.Time) error {
	if len(oldHash) != 32 || len(nextHash) != 32 || len(csrfHash) != 32 || principalID == "" || now.IsZero() {
		return errors.New("OAuth authorization continuation is invalid")
	}
	result, err := repository.executor.ExecContext(ctx, repository.bind(`UPDATE oauth_authorization_requests
		SET challenge_hash = ?, csrf_hash = ?, principal_id = ?, upstream_state_hash = NULL
		WHERE challenge_hash = ? AND status = 'pending' AND expires_at > ?`),
		nextHash, csrfHash, principalID, oldHash, formatTime(now))
	if err != nil {
		return mapWriteError(err)
	}
	count, err := rowsAffected(result)
	if err == nil && count != 1 {
		return ErrNotFound
	}
	return err
}

func (repository *oauthAuthorizationRequestRepository) Get(ctx context.Context, hash []byte, now time.Time) (OAuthAuthorizationRequest, error) {
	return repository.get(ctx, hash, now, false)
}

func (repository *oauthAuthorizationRequestRepository) Consume(ctx context.Context, hash []byte, now time.Time) (OAuthAuthorizationRequest, error) {
	result, err := repository.executor.ExecContext(ctx, repository.bind(`UPDATE oauth_authorization_requests SET status = 'consumed'
		WHERE challenge_hash = ? AND status = 'pending' AND expires_at > ?`), hash, formatTime(now))
	if err != nil {
		return OAuthAuthorizationRequest{}, mapWriteError(err)
	}
	count, err := rowsAffected(result)
	if err != nil {
		return OAuthAuthorizationRequest{}, err
	}
	if count != 1 {
		return OAuthAuthorizationRequest{}, ErrNotFound
	}
	return repository.get(ctx, hash, now, true)
}

func (repository *oauthAuthorizationRequestRepository) get(ctx context.Context, hash []byte, now time.Time, consumed bool) (OAuthAuthorizationRequest, error) {
	status := "pending"
	if consumed {
		status = "consumed"
	}
	var value OAuthAuthorizationRequest
	var raw, created, expires string
	var principal sql.NullString
	err := repository.executor.QueryRowContext(ctx, repository.bind(`SELECT challenge_hash, upstream_state_hash, request_id, request_json, csrf_hash,
		principal_id, provider_id, status, created_at, expires_at FROM oauth_authorization_requests
		WHERE challenge_hash = ? AND status = ? AND expires_at > ?`), hash, status, formatTime(now)).Scan(
		&value.ChallengeHash, &value.UpstreamStateHash, &value.RequestID, &raw, &value.CSRFHash, &principal, &value.ProviderID,
		&value.Status, &created, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return OAuthAuthorizationRequest{}, ErrNotFound
	}
	if err != nil {
		return OAuthAuthorizationRequest{}, databaseError("read OAuth authorization request", err)
	}
	value.RequestJSON = json.RawMessage(raw)
	value.PrincipalID = principal.String
	value.CreatedAt, err = parseTime(created, "OAuth authorization request creation time")
	if err == nil {
		value.ExpiresAt, err = parseTime(expires, "OAuth authorization request expiry")
	}
	return value, err
}

func (repository *oauthAuthorizationRequestRepository) DeleteExpired(ctx context.Context, now time.Time, limit int) (int64, error) {
	if _, err := boundedLimit(limit); err != nil {
		return 0, err
	}
	result, err := repository.executor.ExecContext(ctx, repository.bind(`DELETE FROM oauth_authorization_requests WHERE expires_at <= ?`), formatTime(now))
	if err != nil {
		return 0, mapWriteError(err)
	}
	return rowsAffected(result)
}
