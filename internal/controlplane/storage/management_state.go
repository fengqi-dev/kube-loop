package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type managementStateRepository struct {
	repositoryBase
}

var _ ManagementStateRepository = (*managementStateRepository)(nil)

func (repository *managementStateRepository) BootstrapRetired(ctx context.Context) (bool, error) {
	if repository == nil {
		return false, errors.New("management state repository is unavailable")
	}
	var retiredAt sql.NullString
	err := repository.executor.QueryRowContext(ctx, repository.bind(
		`SELECT bootstrap_retired_at FROM management_metadata WHERE id = ?`,
	), 1).Scan(&retiredAt)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, databaseError("read management bootstrap state", err)
	}
	return retiredAt.Valid && retiredAt.String != "", nil
}

func (repository *managementStateRepository) RetireBootstrap(
	ctx context.Context,
	retiredAt time.Time,
) (bool, error) {
	if repository == nil {
		return false, errors.New("management state repository is unavailable")
	}
	if retiredAt.IsZero() {
		return false, errors.New("management bootstrap retirement time is required")
	}
	retiredAt = retiredAt.UTC()
	query := repository.bind(`
		INSERT INTO management_metadata (
			id, bootstrap_retired_at, updated_at
		) VALUES (?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			bootstrap_retired_at = excluded.bootstrap_retired_at,
			updated_at = excluded.updated_at
		WHERE management_metadata.bootstrap_retired_at IS NULL
	`)
	if repository.backend == BackendMySQL {
		query = `INSERT INTO management_metadata (
			id, bootstrap_retired_at, updated_at
		) VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE
			updated_at = IF(bootstrap_retired_at IS NULL, VALUES(updated_at), updated_at),
			bootstrap_retired_at = IF(bootstrap_retired_at IS NULL, VALUES(bootstrap_retired_at), bootstrap_retired_at)`
	}
	result, err := repository.executor.ExecContext(ctx, query, 1, formatTime(retiredAt), formatTime(retiredAt))
	if err != nil {
		return false, mapWriteError(err)
	}
	count, err := rowsAffected(result)
	if err != nil {
		return false, err
	}
	if repository.backend == BackendMySQL {
		return count > 0, nil
	}
	return count == 1, nil
}
