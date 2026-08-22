package storage

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

func (repository *taskRepository) UpdateState(
	ctx context.Context,
	id string,
	expectedState, nextState remotetask.State,
	result json.RawMessage,
	updatedAt time.Time,
) error {
	if err := validateUUID(id, "task ID"); err != nil {
		return err
	}
	if err := remotetask.ValidateTransition(expectedState, nextState); err != nil {
		return err
	}
	if updatedAt.IsZero() {
		return errors.New(
			"expected state, next state and update time are required",
		)
	}
	var err error
	if result, err = normalizeJSON(result, false, "task result"); err != nil {
		return err
	}
	query := repository.bind(
		`UPDATE tasks SET state = ?, result_json = ?, updated_at = ? WHERE id = ? AND state = ?`,
	)
	if repository.backend == BackendPostgreSQL {
		query = `UPDATE tasks SET state = $1, result_json = $2::jsonb, updated_at = $3 WHERE id = $4 AND state = $5`
	}
	writeResult, err := repository.executor.ExecContext(
		ctx,
		query,
		nextState,
		nullableJSON(result),
		formatTime(updatedAt),
		id,
		expectedState,
	)
	if err != nil {
		return mapWriteError(err)
	}
	count, err := rowsAffected(writeResult)
	if err != nil {
		return err
	}
	if count == 1 {
		return nil
	}
	if _, err := repository.GetByID(ctx, id); errors.Is(err, ErrNotFound) {
		return ErrNotFound
	}
	return ErrConflict
}

func (repository *taskRepository) ClaimStale(
	ctx context.Context,
	id string,
	expectedState remotetask.State,
	observedUpdatedAt time.Time,
	nextState remotetask.State,
	result json.RawMessage,
	updatedAt time.Time,
) error {
	if err := validateUUID(id, "task ID"); err != nil {
		return err
	}
	if err := remotetask.ValidateTransition(expectedState, nextState); err != nil {
		return err
	}
	if observedUpdatedAt.IsZero() || updatedAt.IsZero() {
		return errors.New(
			"expected state, observed time, next state and update time are required",
		)
	}
	var err error
	if result, err = normalizeJSON(result, false, "task result"); err != nil {
		return err
	}
	query := repository.bind(
		`UPDATE tasks SET state = ?, result_json = ?, updated_at = ?
		WHERE id = ? AND state = ? AND updated_at = ?`,
	)
	if repository.backend == BackendPostgreSQL {
		query = `UPDATE tasks SET state = $1, result_json = $2::jsonb, updated_at = $3
			WHERE id = $4 AND state = $5 AND updated_at = $6`
	}
	writeResult, err := repository.executor.ExecContext(
		ctx,
		query,
		nextState,
		nullableJSON(result),
		formatTime(updatedAt),
		id,
		expectedState,
		formatTime(observedUpdatedAt),
	)
	if err != nil {
		return mapWriteError(err)
	}
	count, err := rowsAffected(writeResult)
	if err != nil {
		return err
	}
	if count == 1 {
		return nil
	}
	if _, err := repository.GetByID(ctx, id); errors.Is(err, ErrNotFound) {
		return ErrNotFound
	}
	return ErrConflict
}
