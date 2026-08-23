package storage

import (
	"context"
	"database/sql"
	"errors"
)

type identityRepository struct{ repositoryBase }

func (repository *identityRepository) Create(
	ctx context.Context,
	identity Identity,
) (Identity, error) {
	if err := normalizeIdentity(&identity); err != nil {
		return Identity{}, err
	}
	query := repository.bind(
		`INSERT INTO identities(id, type, display_name, primary_email, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
	)
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

func (repository *identityRepository) Update(
	ctx context.Context,
	identity Identity,
) error {
	if err := normalizeIdentity(&identity); err != nil {
		return err
	}
	result, err := repository.executor.ExecContext(
		ctx,
		repository.bind(
			`UPDATE identities SET type = ?, display_name = ?, primary_email = ?, status = ?, updated_at = ? WHERE id = ?`,
		),
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

func (repository *identityRepository) GetByID(
	ctx context.Context,
	id string,
) (Identity, error) {
	if err := validateUUID(id, "identity ID"); err != nil {
		return Identity{}, err
	}
	identity, err := scanIdentity(repository.executor.QueryRowContext(
		ctx,
		repository.bind(
			`SELECT id, type, display_name, primary_email, status, created_at, updated_at FROM identities WHERE id = ?`,
		),
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
