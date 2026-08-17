package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

type identityRepository struct{ repositoryBase }

func (repository *identityRepository) Create(ctx context.Context, identity Identity) (Identity, error) {
	if err := normalizeIdentity(&identity); err != nil {
		return Identity{}, err
	}
	query := repository.bind(`INSERT INTO identities(id, type, display_name, primary_email, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`)
	_, err := repository.executor.ExecContext(
		ctx,
		query,
		identity.ID,
		identity.Type,
		identity.DisplayName,
		identity.PrimaryEmail,
		identity.Status,
		formatTime(identity.CreatedAt),
		formatTime(identity.UpdatedAt),
	)
	if err != nil {
		return Identity{}, mapWriteError(err)
	}
	return identity, nil
}

func (repository *identityRepository) Update(ctx context.Context, identity Identity) error {
	if err := normalizeIdentity(&identity); err != nil {
		return err
	}
	result, err := repository.executor.ExecContext(
		ctx,
		repository.bind(`UPDATE identities SET type = ?, display_name = ?, primary_email = ?, status = ?, updated_at = ? WHERE id = ?`),
		identity.Type,
		identity.DisplayName,
		identity.PrimaryEmail,
		identity.Status,
		formatTime(identity.UpdatedAt),
		identity.ID,
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

func (repository *identityRepository) GetByID(ctx context.Context, id string) (Identity, error) {
	if err := validateUUID(id, "identity ID"); err != nil {
		return Identity{}, err
	}
	identity, err := scanIdentity(repository.executor.QueryRowContext(
		ctx,
		repository.bind(`SELECT id, type, display_name, primary_email, status, created_at, updated_at FROM identities WHERE id = ?`),
		id,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return Identity{}, ErrNotFound
	}
	if err != nil {
		return Identity{}, databaseError("read identity", err)
	}
	return identity, nil
}

func (repository *identityRepository) List(ctx context.Context, filter IdentityListFilter) ([]Identity, error) {
	limit, cursor, err := normalizePage(filter.Limit, filter.Cursor)
	if err != nil {
		return nil, err
	}
	filter.Type = strings.TrimSpace(filter.Type)
	filter.Status = strings.TrimSpace(filter.Status)
	filter.Search = strings.TrimSpace(filter.Search)
	if filter.Type != "" && filter.Type != "human" && filter.Type != "machine" {
		return nil, errors.New("identity type filter is invalid")
	}
	if filter.Status != "" && !validIdentityStatus(filter.Status) {
		return nil, errors.New("identity status filter is invalid")
	}
	if len(filter.Search) > 256 || strings.ContainsAny(filter.Search, "\x00\r\n") {
		return nil, errors.New("identity search filter is invalid")
	}
	query := `SELECT id, type, display_name, primary_email, status, created_at, updated_at FROM identities WHERE 1=1`
	arguments := make([]any, 0, 7)
	if filter.Type != "" {
		query += ` AND type = ?`
		arguments = append(arguments, filter.Type)
	}
	if filter.Status != "" {
		query += ` AND status = ?`
		arguments = append(arguments, filter.Status)
	}
	if filter.Search != "" {
		query += ` AND (LOWER(display_name) LIKE ? OR LOWER(primary_email) LIKE ?)`
		search := "%" + strings.ToLower(filter.Search) + "%"
		arguments = append(arguments, search, search)
	}
	query, arguments = appendPageBoundary(query, arguments, "", cursor)
	query += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	arguments = append(arguments, limit)
	rows, err := repository.executor.QueryContext(ctx, repository.bind(query), arguments...)
	if err != nil {
		return nil, databaseError("list identities", err)
	}
	defer rows.Close()
	identities := make([]Identity, 0)
	for rows.Next() {
		identity, scanErr := scanIdentity(rows)
		if scanErr != nil {
			return nil, databaseError("decode identity", scanErr)
		}
		identities = append(identities, identity)
	}
	if err := rows.Err(); err != nil {
		return nil, databaseError("iterate identities", err)
	}
	return identities, nil
}

func normalizeIdentity(identity *Identity) error {
	if _, err := uuid.Parse(identity.ID); err != nil {
		return errors.New("identity ID must be a UUID")
	}
	identity.Type = strings.TrimSpace(identity.Type)
	identity.DisplayName = strings.TrimSpace(identity.DisplayName)
	identity.PrimaryEmail = strings.ToLower(strings.TrimSpace(identity.PrimaryEmail))
	identity.Status = strings.TrimSpace(identity.Status)
	if identity.Type != "human" && identity.Type != "machine" {
		return errors.New("identity type is invalid")
	}
	if identity.DisplayName == "" || len(identity.DisplayName) > 256 || len(identity.PrimaryEmail) > 320 {
		return errors.New("identity profile is invalid")
	}
	if !validIdentityStatus(identity.Status) {
		return errors.New("identity status is invalid")
	}
	now := time.Now().UTC()
	if identity.CreatedAt.IsZero() {
		identity.CreatedAt = now
	}
	if identity.UpdatedAt.IsZero() {
		identity.UpdatedAt = identity.CreatedAt
	}
	identity.CreatedAt = identity.CreatedAt.UTC()
	identity.UpdatedAt = identity.UpdatedAt.UTC()
	if identity.UpdatedAt.Before(identity.CreatedAt) {
		return errors.New("identity timestamps are invalid")
	}
	return nil
}

func validIdentityStatus(status string) bool {
	return status == "active" || status == "suspended" || status == "disabled"
}

func scanIdentity(row rowScanner) (Identity, error) {
	var identity Identity
	var createdAt, updatedAt string
	if err := row.Scan(
		&identity.ID,
		&identity.Type,
		&identity.DisplayName,
		&identity.PrimaryEmail,
		&identity.Status,
		&createdAt,
		&updatedAt,
	); err != nil {
		return Identity{}, err
	}
	var err error
	identity.CreatedAt, err = parseTime(createdAt, "identity creation time")
	if err == nil {
		identity.UpdatedAt, err = parseTime(updatedAt, "identity update time")
	}
	return identity, err
}
