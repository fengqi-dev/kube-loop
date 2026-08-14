package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

type activeManagementConfigRepository struct{ repositoryBase }

func (repository *activeManagementConfigRepository) List(ctx context.Context, kind string) ([]ActiveManagementConfig, error) {
	kind = strings.TrimSpace(kind)
	if kind != ManagementConfigurationPolicy && kind != ManagementConfigurationProvider {
		return nil, errors.New("management configuration type is invalid")
	}
	query := repository.bind(`SELECT configuration_type, configuration_id, object_id, updated_by,
		updated_authentication_type, updated_at FROM management_active_configs
		WHERE configuration_type = ? ORDER BY configuration_id ASC`)
	rows, err := repository.executor.QueryContext(ctx, query, kind)
	if err != nil {
		return nil, databaseError("list active management configurations", err)
	}
	defer rows.Close()
	result := make([]ActiveManagementConfig, 0)
	for rows.Next() {
		active, scanErr := scanActiveManagementConfig(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, active)
	}
	if err := rows.Err(); err != nil {
		return nil, databaseError("iterate active management configurations", err)
	}
	return result, nil
}

func (repository *activeManagementConfigRepository) Get(ctx context.Context, kind, configurationID string) (ActiveManagementConfig, error) {
	kind, configurationID, err := normalizeConfigurationIdentity(kind, configurationID)
	if err != nil {
		return ActiveManagementConfig{}, err
	}
	query := repository.bind(`SELECT configuration_type, configuration_id, object_id, updated_by,
		updated_authentication_type, updated_at FROM management_active_configs
		WHERE configuration_type = ? AND configuration_id = ?`)
	active, err := scanActiveManagementConfig(repository.executor.QueryRowContext(ctx, query, kind, configurationID))
	if errors.Is(err, sql.ErrNoRows) {
		return ActiveManagementConfig{}, ErrNotFound
	}
	if err != nil {
		return ActiveManagementConfig{}, databaseError("read active management configuration", err)
	}
	return active, nil
}

func (repository *activeManagementConfigRepository) Set(
	ctx context.Context,
	kind, configurationID, objectID, updatedBy, updatedAuthenticationType string,
	updatedAt time.Time,
) (ActiveManagementConfig, error) {
	kind, configurationID, err := normalizeConfigurationIdentity(kind, configurationID)
	if err != nil {
		return ActiveManagementConfig{}, err
	}
	if validateUUID(strings.TrimSpace(objectID), "management configuration object ID") != nil || updatedAt.IsZero() {
		return ActiveManagementConfig{}, errors.New("active management configuration values are invalid")
	}
	updatedBy, updatedAuthenticationType, err = normalizeManagementActor(updatedBy, updatedAuthenticationType)
	if err != nil {
		return ActiveManagementConfig{}, err
	}
	if err := repository.requireValidTarget(ctx, kind, configurationID, objectID); err != nil {
		return ActiveManagementConfig{}, err
	}
	updatedAt = updatedAt.UTC()
	query := repository.bind(`UPDATE management_active_configs SET object_id = ?, updated_by = ?,
		updated_authentication_type = ?, updated_at = ? WHERE configuration_type = ? AND configuration_id = ?`)
	result, err := repository.executor.ExecContext(ctx, query, objectID, updatedBy,
		updatedAuthenticationType, formatTime(updatedAt), kind, configurationID)
	if err != nil {
		return ActiveManagementConfig{}, mapWriteError(err)
	}
	count, err := rowsAffected(result)
	if err != nil {
		return ActiveManagementConfig{}, err
	}
	if count == 0 {
		query = repository.bind(`INSERT INTO management_active_configs(
			configuration_type, configuration_id, object_id, updated_by, updated_authentication_type, updated_at
		) VALUES (?, ?, ?, ?, ?, ?)`)
		if _, err := repository.executor.ExecContext(ctx, query, kind, configurationID, objectID,
			updatedBy, updatedAuthenticationType, formatTime(updatedAt)); err != nil {
			return ActiveManagementConfig{}, mapWriteError(err)
		}
	}
	return repository.Get(ctx, kind, configurationID)
}

func (repository *activeManagementConfigRepository) requireValidTarget(ctx context.Context, kind, configurationID, objectID string) error {
	var validationState string
	var err error
	if kind == ManagementConfigurationPolicy {
		query := repository.bind(`SELECT validation_state FROM authorization_policies WHERE id = ?`)
		err = repository.executor.QueryRowContext(ctx, query, objectID).Scan(&validationState)
	} else {
		query := repository.bind(`SELECT validation_state FROM provider_configs WHERE id = ? AND provider_id = ?`)
		err = repository.executor.QueryRowContext(ctx, query, objectID, configurationID).Scan(&validationState)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return databaseError("validate active management configuration target", err)
	}
	if validationState != ConfigValidationValid {
		return ErrConflict
	}
	return nil
}

func scanActiveManagementConfig(row rowScanner) (ActiveManagementConfig, error) {
	var active ActiveManagementConfig
	var updatedAt string
	if err := row.Scan(&active.ConfigurationType, &active.ConfigurationID, &active.ObjectID,
		&active.UpdatedBy, &active.UpdatedAuthenticationType, &updatedAt); err != nil {
		return ActiveManagementConfig{}, err
	}
	var err error
	if active.UpdatedAt, err = parseTime(updatedAt, "active management configuration update time"); err != nil {
		return ActiveManagementConfig{}, err
	}
	return active, nil
}
