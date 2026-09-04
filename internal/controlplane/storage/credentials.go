package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/utils"
)

type credentialRepository struct{ repositoryBase }

func (repository *credentialRepository) CreatePassword(
	ctx context.Context,
	credential PasswordCredential,
) error {
	if err := normalizePasswordCredential(&credential); err != nil {
		return err
	}
	_, err := repository.executor.ExecContext(
		ctx,
		repository.bind(`INSERT INTO password_credentials(
		identity_id, username, password_hash, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`),
		credential.IdentityID,
		credential.Username,
		credential.PasswordHash,
		credential.Enabled,
		formatTime(credential.CreatedAt),
		formatTime(credential.UpdatedAt),
	)
	return mapWriteError(err)
}

func (repository *credentialRepository) GetPasswordByIdentity(
	ctx context.Context,
	identityID string,
) (PasswordCredential, error) {
	if _, err := uuid.Parse(identityID); err != nil {
		return PasswordCredential{}, errors.New(
			"credential identity ID must be a UUID",
		)
	}
	return repository.getPassword(ctx, `WHERE identity_id = ?`, identityID)
}

func (repository *credentialRepository) GetPasswordByUsername(
	ctx context.Context,
	username string,
) (PasswordCredential, error) {
	username = utils.NormalizeUsername(username)
	if username == "" {
		return PasswordCredential{}, errors.New(
			"credential username is required",
		)
	}
	return repository.getPassword(ctx, `WHERE username = ?`, username)
}

func (repository *credentialRepository) getPassword(
	ctx context.Context,
	where string,
	value any,
) (PasswordCredential, error) {
	var credential PasswordCredential
	var createdAt, updatedAt string
	err := repository.executor.QueryRowContext(ctx, repository.bind(`SELECT identity_id, username, password_hash,
		enabled, created_at, updated_at FROM password_credentials `+where), value).
		Scan(
			&credential.IdentityID, &credential.Username, &credential.PasswordHash, &credential.Enabled,
			&createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PasswordCredential{}, ErrNotFound
	}
	if err != nil {
		return PasswordCredential{}, databaseError(
			"read password credential",
			err,
		)
	}
	credential.CreatedAt, err = parseTime(
		createdAt,
		"password credential creation time",
	)
	if err == nil {
		credential.UpdatedAt, err = parseTime(
			updatedAt,
			"password credential update time",
		)
	}
	return credential, err
}

func (repository *credentialRepository) UpdatePassword(
	ctx context.Context,
	identityID, passwordHash string,
	at time.Time,
) error {
	return repository.updatePassword(
		ctx,
		identityID,
		at,
		`password_hash = ?`,
		strings.TrimSpace(passwordHash),
	)
}

func (repository *credentialRepository) SetPasswordEnabled(
	ctx context.Context,
	identityID string,
	enabled bool,
	at time.Time,
) error {
	return repository.updatePassword(
		ctx,
		identityID,
		at,
		`enabled = ?`,
		enabled,
	)
}

func (repository *credentialRepository) updatePassword(
	ctx context.Context,
	identityID string,
	at time.Time,
	set string,
	values ...any,
) error {
	if _, err := uuid.Parse(identityID); err != nil || at.IsZero() {
		return errors.New("password credential update is invalid")
	}
	query := repository.bind(
		`UPDATE password_credentials SET ` + set + `, updated_at = ? WHERE identity_id = ?`,
	)
	arguments := make([]any, 0, len(values)+2)
	arguments = append(arguments, values...)
	arguments = append(arguments, formatTime(at), identityID)
	result, err := repository.executor.ExecContext(ctx, query, arguments...)
	if err != nil {
		return mapWriteError(err)
	}
	return expectOne(result)
}

func normalizePasswordCredential(credential *PasswordCredential) error {
	if _, err := uuid.Parse(credential.IdentityID); err != nil {
		return errors.New("password credential identity ID must be a UUID")
	}
	credential.Username = utils.NormalizeUsername(credential.Username)
	credential.PasswordHash = strings.TrimSpace(credential.PasswordHash)
	if credential.Username == "" || len(credential.Username) > 128 ||
		strings.ContainsAny(credential.Username, "\x00\r\n\t ") ||
		credential.PasswordHash == "" {
		return errors.New("password credential is invalid")
	}
	if credential.CreatedAt.IsZero() || credential.UpdatedAt.IsZero() ||
		credential.UpdatedAt.Before(credential.CreatedAt) {
		return errors.New("password credential timestamps are invalid")
	}
	credential.CreatedAt = credential.CreatedAt.UTC()
	credential.UpdatedAt = credential.UpdatedAt.UTC()
	return nil
}
