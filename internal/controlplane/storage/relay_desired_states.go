package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

type relayDesiredStateRepository struct{ repositoryBase }

func (repository *relayDesiredStateRepository) Get(
	ctx context.Context,
	relayID string,
) (RelayDesiredState, error) {
	if err := validateRelayDesiredStateID(relayID); err != nil {
		return RelayDesiredState{}, err
	}
	query := repository.bind(
		`SELECT relay_id, desired_state, version, updated_by,
		updated_authentication_type, reason, updated_at FROM relay_desired_states WHERE relay_id = ?`,
	)
	value, err := scanRelayDesiredState(
		repository.executor.QueryRowContext(ctx, query, relayID),
	)
	if errors.Is(err, sql.ErrNoRows) {
		return RelayDesiredState{}, ErrNotFound
	}
	if err != nil {
		return RelayDesiredState{}, databaseError(
			"read Relay desired state",
			err,
		)
	}
	return value, nil
}

func (repository *relayDesiredStateRepository) List(
	ctx context.Context,
) ([]RelayDesiredState, error) {
	rows, err := repository.executor.QueryContext(
		ctx,
		`SELECT relay_id, desired_state, version, updated_by,
		updated_authentication_type, reason, updated_at FROM relay_desired_states ORDER BY relay_id`,
	)
	if err != nil {
		return nil, databaseError("list Relay desired states", err)
	}
	defer func() { _ = rows.Close() }()
	result := make([]RelayDesiredState, 0)
	for rows.Next() {
		value, scanErr := scanRelayDesiredState(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, databaseError("iterate Relay desired states", err)
	}
	return result, nil
}

func (repository *relayDesiredStateRepository) CompareAndSwap(
	ctx context.Context,
	relayID, desiredState string,
	expectedVersion uint64,
	updatedBy, authenticationType, reason string,
	updatedAt time.Time,
) (RelayDesiredState, error) {
	value := RelayDesiredState{
		RelayID: relayID, DesiredState: desiredState, Version: expectedVersion + 1,
		UpdatedBy: updatedBy, UpdatedAuthenticationType: authenticationType, Reason: reason, UpdatedAt: updatedAt,
	}
	if err := normalizeRelayDesiredState(&value); err != nil {
		return RelayDesiredState{}, err
	}
	var result sql.Result
	var err error
	if expectedVersion == 0 {
		query := repository.bind(
			`INSERT INTO relay_desired_states(relay_id, desired_state, version,
			updated_by, updated_authentication_type, reason, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		)
		result, err = repository.executor.ExecContext(
			ctx,
			query,
			value.RelayID,
			value.DesiredState,
			value.Version,
			value.UpdatedBy,
			value.UpdatedAuthenticationType,
			value.Reason,
			formatTime(value.UpdatedAt),
		)
	} else {
		query := repository.bind(`UPDATE relay_desired_states SET desired_state = ?, version = ?, updated_by = ?,
			updated_authentication_type = ?, reason = ?, updated_at = ? WHERE relay_id = ? AND version = ?`)
		result, err = repository.executor.ExecContext(ctx, query, value.DesiredState, value.Version, value.UpdatedBy,
			value.UpdatedAuthenticationType, value.Reason, formatTime(value.UpdatedAt), value.RelayID, expectedVersion)
	}
	if err != nil {
		return RelayDesiredState{}, mapWriteError(err)
	}
	count, err := rowsAffected(result)
	if err != nil {
		return RelayDesiredState{}, err
	}
	if count != 1 {
		return RelayDesiredState{}, ErrConflict
	}
	return value, nil
}

func normalizeRelayDesiredState(value *RelayDesiredState) error {
	if err := validateRelayDesiredStateID(value.RelayID); err != nil {
		return err
	}
	value.DesiredState = strings.TrimSpace(value.DesiredState)
	value.UpdatedBy = strings.TrimSpace(value.UpdatedBy)
	value.UpdatedAuthenticationType = strings.TrimSpace(
		value.UpdatedAuthenticationType,
	)
	value.Reason = strings.TrimSpace(value.Reason)
	if (value.DesiredState != "ready" && value.DesiredState != "draining") || value.Version == 0 ||
		value.UpdatedBy == "" || value.Reason == "" ||
		value.UpdatedAt.IsZero() {
		return errors.New("relay desired state is invalid")
	}
	switch value.UpdatedAuthenticationType {
	case sessionKindNormal, "bootstrap":
	default:
		return errors.New("relay desired state authentication type is invalid")
	}
	value.UpdatedAt = value.UpdatedAt.UTC()
	return nil
}

func validateRelayDesiredStateID(value string) error {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 256 ||
		strings.ContainsAny(value, "\x00\r\n/\\") {
		return errors.New("relay ID is invalid")
	}
	return nil
}

func scanRelayDesiredState(row rowScanner) (RelayDesiredState, error) {
	var value RelayDesiredState
	var updatedAt string
	if err := row.Scan(&value.RelayID, &value.DesiredState, &value.Version,
		&value.UpdatedBy, &value.UpdatedAuthenticationType, &value.Reason, &updatedAt); err != nil {
		return RelayDesiredState{}, err
	}
	var err error
	value.UpdatedAt, err = parseTime(
		updatedAt,
		"Relay desired state update time",
	)
	return value, err
}
