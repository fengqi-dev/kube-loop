package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

type activeManagementRevisionRepository struct{ repositoryBase }

func (repository *activeManagementRevisionRepository) List(
	ctx context.Context, kind string,
) ([]ActiveManagementRevision, error) {
	kind = strings.TrimSpace(kind)
	if kind != ManagementConfigurationPolicy && kind != ManagementConfigurationProvider {
		return nil, errors.New("management configuration type is invalid")
	}
	query := repository.bind(`SELECT configuration_type, configuration_id, revision, etag, updated_by,
		updated_authentication_type, updated_at FROM management_active_revisions
		WHERE configuration_type = ? ORDER BY configuration_id ASC`)
	rows, err := repository.executor.QueryContext(ctx, query, kind)
	if err != nil {
		return nil, databaseError("list active management revisions", err)
	}
	defer rows.Close()
	result := make([]ActiveManagementRevision, 0)
	for rows.Next() {
		active, scanErr := scanActiveManagementRevision(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, active)
	}
	if err := rows.Err(); err != nil {
		return nil, databaseError("iterate active management revisions", err)
	}
	return result, nil
}

func (repository *activeManagementRevisionRepository) Get(
	ctx context.Context, kind, configurationID string,
) (ActiveManagementRevision, error) {
	kind, configurationID, err := normalizeConfigurationIdentity(kind, configurationID)
	if err != nil {
		return ActiveManagementRevision{}, err
	}
	query := repository.bind(`SELECT configuration_type, configuration_id, revision, etag, updated_by,
		updated_authentication_type, updated_at
		FROM management_active_revisions WHERE configuration_type = ? AND configuration_id = ?`)
	active, err := scanActiveManagementRevision(repository.executor.QueryRowContext(ctx, query, kind, configurationID))
	if errors.Is(err, sql.ErrNoRows) {
		return ActiveManagementRevision{}, ErrNotFound
	}
	if err != nil {
		return ActiveManagementRevision{}, databaseError("read active management revision", err)
	}
	return active, nil
}

func (repository *activeManagementRevisionRepository) CompareAndSwap(
	ctx context.Context,
	kind, configurationID string,
	revision, expectedETag uint64,
	updatedBy, updatedAuthenticationType string,
	updatedAt time.Time,
) (ActiveManagementRevision, error) {
	kind, configurationID, err := normalizeConfigurationIdentity(kind, configurationID)
	if err != nil {
		return ActiveManagementRevision{}, err
	}
	if revision == 0 || updatedAt.IsZero() {
		return ActiveManagementRevision{}, errors.New("active management revision values are invalid")
	}
	updatedBy, updatedAuthenticationType, err = normalizeManagementActor(updatedBy, updatedAuthenticationType)
	if err != nil {
		return ActiveManagementRevision{}, err
	}
	if err := repository.requireValidTarget(ctx, kind, configurationID, revision); err != nil {
		return ActiveManagementRevision{}, err
	}
	updatedAt = updatedAt.UTC()
	if expectedETag == 0 {
		query := repository.bind(`INSERT INTO management_active_revisions(
			configuration_type, configuration_id, revision, etag, updated_by, updated_authentication_type, updated_at
		) VALUES (?, ?, ?, 1, ?, ?, ?) ON CONFLICT(configuration_type, configuration_id) DO NOTHING`)
		result, err := repository.executor.ExecContext(
			ctx, query, kind, configurationID, revision, updatedBy, updatedAuthenticationType, formatTime(updatedAt),
		)
		if err != nil {
			return ActiveManagementRevision{}, mapWriteError(err)
		}
		count, err := rowsAffected(result)
		if err != nil {
			return ActiveManagementRevision{}, err
		}
		if count != 1 {
			return ActiveManagementRevision{}, ErrConflict
		}
	} else {
		query := repository.bind(`UPDATE management_active_revisions SET
			revision = ?, etag = etag + 1, updated_by = ?, updated_authentication_type = ?, updated_at = ?
			WHERE configuration_type = ? AND configuration_id = ? AND etag = ?`)
		result, err := repository.executor.ExecContext(ctx, query,
			revision, updatedBy, updatedAuthenticationType, formatTime(updatedAt), kind, configurationID, expectedETag,
		)
		if err != nil {
			return ActiveManagementRevision{}, mapWriteError(err)
		}
		count, err := rowsAffected(result)
		if err != nil {
			return ActiveManagementRevision{}, err
		}
		if count != 1 {
			return ActiveManagementRevision{}, ErrConflict
		}
	}
	return repository.Get(ctx, kind, configurationID)
}

func (repository *activeManagementRevisionRepository) requireValidTarget(
	ctx context.Context, kind, configurationID string, revision uint64,
) error {
	var validationState string
	var err error
	if kind == ManagementConfigurationPolicy {
		query := repository.bind(`SELECT validation_state FROM admin_policy_revisions WHERE revision = ?`)
		err = repository.executor.QueryRowContext(ctx, query, revision).Scan(&validationState)
	} else {
		query := repository.bind(`SELECT validation_state FROM provider_config_revisions
			WHERE revision = ? AND provider_id = ?`)
		err = repository.executor.QueryRowContext(ctx, query, revision, configurationID).Scan(&validationState)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return databaseError("validate active management revision target", err)
	}
	if validationState != RevisionValidationValid {
		return ErrConflict
	}
	return nil
}

func scanActiveManagementRevision(row rowScanner) (ActiveManagementRevision, error) {
	var active ActiveManagementRevision
	var updatedAt string
	if err := row.Scan(&active.ConfigurationType, &active.ConfigurationID, &active.Revision,
		&active.ETag, &active.UpdatedBy, &active.UpdatedAuthenticationType, &updatedAt); err != nil {
		return ActiveManagementRevision{}, err
	}
	var err error
	if active.UpdatedAt, err = parseTime(updatedAt, "active management revision update time"); err != nil {
		return ActiveManagementRevision{}, err
	}
	return active, nil
}
