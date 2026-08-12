package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

type localAdminUserRepository struct{ repositoryBase }

func (repository *localAdminUserRepository) Create(ctx context.Context, user LocalAdminUser) error {
	if err := normalizeLocalAdminUser(&user); err != nil {
		return err
	}
	query := repository.bind(`INSERT INTO local_admin_users (
		principal_id, schema_version, username, password_hash, enabled,
		totp_secret_encrypted, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err := repository.executor.ExecContext(ctx, query, user.PrincipalID, user.SchemaVersion, user.Username,
		user.PasswordHash, user.Enabled, nullableBytes(user.TOTPSecretEncrypted),
		formatTime(user.CreatedAt), formatTime(user.UpdatedAt))
	return mapWriteError(err)
}

func (repository *localAdminUserRepository) GetByPrincipalID(ctx context.Context, principalID string) (LocalAdminUser, error) {
	if _, err := uuid.Parse(principalID); err != nil {
		return LocalAdminUser{}, errors.New("local administrator principal ID must be a UUID")
	}
	return repository.get(ctx, `WHERE principal_id = ?`, principalID)
}

func (repository *localAdminUserRepository) GetByUsername(ctx context.Context, username string) (LocalAdminUser, error) {
	username = normalizeUsername(username)
	if username == "" {
		return LocalAdminUser{}, errors.New("local administrator username is required")
	}
	return repository.get(ctx, `WHERE username = ?`, username)
}

func (repository *localAdminUserRepository) get(ctx context.Context, where string, value any) (LocalAdminUser, error) {
	query := repository.bind(`SELECT principal_id, schema_version, username, password_hash, enabled,
		totp_secret_encrypted, bootstrap_complete, created_at, updated_at FROM local_admin_users ` + where)
	user, err := scanLocalAdminUser(repository.executor.QueryRowContext(ctx, query, value))
	if errors.Is(err, sql.ErrNoRows) {
		return LocalAdminUser{}, ErrNotFound
	}
	if err != nil {
		return LocalAdminUser{}, databaseError("read local administrator", err)
	}
	return user, nil
}

func (repository *localAdminUserRepository) List(ctx context.Context) ([]LocalAdminUser, error) {
	rows, err := repository.executor.QueryContext(ctx, `SELECT principal_id, schema_version, username, password_hash, enabled,
		totp_secret_encrypted, bootstrap_complete, created_at, updated_at FROM local_admin_users ORDER BY username`)
	if err != nil {
		return nil, databaseError("list local administrators", err)
	}
	defer rows.Close()
	users := make([]LocalAdminUser, 0)
	for rows.Next() {
		user, scanErr := scanLocalAdminUser(rows)
		if scanErr != nil {
			return nil, databaseError("scan local administrator", scanErr)
		}
		users = append(users, user)
	}
	return users, databaseError("iterate local administrators", rows.Err())
}

func (repository *localAdminUserRepository) UpdatePassword(ctx context.Context, principalID, passwordHash string, at time.Time) error {
	return repository.update(ctx, principalID, at, `password_hash = ?`, strings.TrimSpace(passwordHash))
}

func (repository *localAdminUserRepository) UpdateEnabled(ctx context.Context, principalID string, enabled bool, at time.Time) error {
	return repository.update(ctx, principalID, at, `enabled = ?`, enabled)
}

func (repository *localAdminUserRepository) UpdateTOTP(ctx context.Context, principalID string, encrypted []byte, at time.Time) error {
	return repository.update(ctx, principalID, at, `totp_secret_encrypted = ?`, nullableBytes(encrypted))
}

func (repository *localAdminUserRepository) MarkBootstrapComplete(ctx context.Context, principalID string, at time.Time) error {
	return repository.update(ctx, principalID, at, `bootstrap_complete = ?`, true)
}

func (repository *localAdminUserRepository) update(ctx context.Context, principalID string, at time.Time, set string, value any) error {
	if _, err := uuid.Parse(principalID); err != nil || at.IsZero() {
		return errors.New("local administrator update is invalid")
	}
	query := repository.bind(`UPDATE local_admin_users SET ` + set + `, updated_at = ? WHERE principal_id = ?`)
	result, err := repository.executor.ExecContext(ctx, query, value, formatTime(at), principalID)
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

func normalizeLocalAdminUser(user *LocalAdminUser) error {
	if _, err := uuid.Parse(user.PrincipalID); err != nil {
		return errors.New("local administrator principal ID must be a UUID")
	}
	user.Username = normalizeUsername(user.Username)
	user.PasswordHash = strings.TrimSpace(user.PasswordHash)
	if user.Username == "" || len(user.Username) > 128 || strings.ContainsAny(user.Username, "\x00\r\n\t ") || user.PasswordHash == "" {
		return errors.New("local administrator credentials are invalid")
	}
	if user.SchemaVersion == 0 {
		user.SchemaVersion = ObjectSchemaVersion
	}
	if user.SchemaVersion != ObjectSchemaVersion || user.CreatedAt.IsZero() || user.UpdatedAt.IsZero() || user.UpdatedAt.Before(user.CreatedAt) {
		return errors.New("local administrator schema or timestamps are invalid")
	}
	user.CreatedAt, user.UpdatedAt = user.CreatedAt.UTC(), user.UpdatedAt.UTC()
	user.TOTPSecretEncrypted = append([]byte(nil), user.TOTPSecretEncrypted...)
	return nil
}

func normalizeUsername(value string) string { return strings.ToLower(strings.TrimSpace(value)) }

func scanLocalAdminUser(row rowScanner) (LocalAdminUser, error) {
	var user LocalAdminUser
	var encrypted []byte
	var createdAt, updatedAt string
	if err := row.Scan(&user.PrincipalID, &user.SchemaVersion, &user.Username, &user.PasswordHash,
		&user.Enabled, &encrypted, &user.BootstrapComplete, &createdAt, &updatedAt); err != nil {
		return LocalAdminUser{}, err
	}
	var err error
	if user.CreatedAt, err = parseTime(createdAt, "local administrator creation time"); err != nil {
		return LocalAdminUser{}, err
	}
	if user.UpdatedAt, err = parseTime(updatedAt, "local administrator update time"); err != nil {
		return LocalAdminUser{}, err
	}
	user.TOTPSecretEncrypted = append([]byte(nil), encrypted...)
	return user, nil
}

type adminRecoveryCodeRepository struct{ repositoryBase }

func (repository *adminRecoveryCodeRepository) Replace(ctx context.Context, principalID string, hashes [][]byte, at time.Time) error {
	if _, err := uuid.Parse(principalID); err != nil || at.IsZero() || len(hashes) == 0 {
		return errors.New("administrator recovery codes are invalid")
	}
	if _, err := repository.executor.ExecContext(ctx, repository.bind(`DELETE FROM admin_recovery_codes WHERE principal_id = ?`), principalID); err != nil {
		return mapWriteError(err)
	}
	query := repository.bind(`INSERT INTO admin_recovery_codes (principal_id, code_hash, created_at) VALUES (?, ?, ?)`)
	for _, hash := range hashes {
		if len(hash) != sha256Size {
			return errors.New("administrator recovery code hash must be a SHA-256 value")
		}
		if _, err := repository.executor.ExecContext(ctx, query, principalID, hash, formatTime(at)); err != nil {
			return mapWriteError(err)
		}
	}
	return nil
}

func (repository *adminRecoveryCodeRepository) Consume(ctx context.Context, principalID string, hash []byte) error {
	if _, err := uuid.Parse(principalID); err != nil || len(hash) != sha256Size {
		return errors.New("administrator recovery code is invalid")
	}
	result, err := repository.executor.ExecContext(ctx, repository.bind(`DELETE FROM admin_recovery_codes WHERE principal_id = ? AND code_hash = ?`), principalID, hash)
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

func (repository *adminRecoveryCodeRepository) DeleteByPrincipal(ctx context.Context, principalID string) error {
	if _, err := uuid.Parse(principalID); err != nil {
		return errors.New("administrator recovery-code principal ID must be a UUID")
	}
	_, err := repository.executor.ExecContext(ctx, repository.bind(`DELETE FROM admin_recovery_codes WHERE principal_id = ?`), principalID)
	return mapWriteError(err)
}

func nullableBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}
