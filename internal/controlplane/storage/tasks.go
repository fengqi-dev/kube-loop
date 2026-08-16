package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

type taskRepository struct {
	repositoryBase
}

func (repository *taskRepository) Create(ctx context.Context, task Task) error {
	if err := normalizeTask(&task); err != nil {
		return err
	}
	query := repository.bind(`INSERT INTO tasks(
		id, identity_id, session_id, type, state, spec_json, result_json,
		idempotency_key, created_at, updated_at, expires_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if repository.backend == BackendPostgreSQL {
		query = `INSERT INTO tasks(
			id, identity_id, session_id, type, state, spec_json, result_json,
			idempotency_key, created_at, updated_at, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb, $8, $9, $10, $11)`
	}
	_, err := repository.executor.ExecContext(ctx, query,
		task.ID, task.IdentityID, task.SessionID, task.Type, task.State,
		string(task.Spec), nullableJSON(task.Result), task.IdempotencyKey,
		formatTime(task.CreatedAt), formatTime(task.UpdatedAt), nullableTime(task.ExpiresAt),
	)
	return mapWriteError(err)
}

func (repository *taskRepository) GetByID(ctx context.Context, id string) (Task, error) {
	if err := validateUUID(id, "task ID"); err != nil {
		return Task{}, err
	}
	query := repository.bind(`SELECT id, identity_id, session_id, type, state, spec_json,
		result_json, idempotency_key, created_at, updated_at, expires_at FROM tasks WHERE id = ?`)
	task, err := scanTask(repository.executor.QueryRowContext(ctx, query, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	if err != nil {
		return Task{}, databaseError("read task", err)
	}
	return task, nil
}

func (repository *taskRepository) List(ctx context.Context, filter TaskListFilter) ([]Task, error) {
	limit, cursor, err := normalizePage(filter.Limit, filter.Cursor)
	if err != nil {
		return nil, err
	}
	filter.IdentityID = strings.TrimSpace(filter.IdentityID)
	filter.SessionID = strings.TrimSpace(filter.SessionID)
	filter.Namespace = strings.TrimSpace(filter.Namespace)
	filter.Type = strings.TrimSpace(filter.Type)
	if filter.IdentityID != "" && validateUUID(filter.IdentityID, "task identity ID") != nil {
		return nil, errors.New("task identity filter is invalid")
	}
	if filter.SessionID != "" && validateUUID(filter.SessionID, "task session ID") != nil {
		return nil, errors.New("task session filter is invalid")
	}
	if filter.Namespace != "" && !dns1123Label.MatchString(filter.Namespace) {
		return nil, errors.New("task namespace filter is invalid")
	}
	if len(filter.Type) > 128 || strings.ContainsAny(filter.Type, "\x00\r\n") {
		return nil, errors.New("task type filter is invalid")
	}
	if filter.State != "" && !filter.State.Valid() {
		return nil, errors.New("task state filter is invalid")
	}
	query := `SELECT t.id, t.identity_id, t.session_id, t.type, t.state, t.spec_json,
		t.result_json, t.idempotency_key, t.created_at, t.updated_at, t.expires_at
		FROM tasks AS t INNER JOIN sessions AS s ON s.id = t.session_id WHERE 1=1`
	arguments := make([]any, 0, 13)
	if filter.IdentityID != "" {
		query += ` AND t.identity_id = ?`
		arguments = append(arguments, filter.IdentityID)
	}
	if filter.SessionID != "" {
		query += ` AND t.session_id = ?`
		arguments = append(arguments, filter.SessionID)
	}
	if filter.Namespace != "" {
		query += ` AND s.namespace = ?`
		arguments = append(arguments, filter.Namespace)
	}
	if filter.Type != "" {
		query += ` AND t.type = ?`
		arguments = append(arguments, filter.Type)
	}
	if filter.State != "" {
		query += ` AND t.state = ?`
		arguments = append(arguments, filter.State)
	}
	query, arguments = appendPageBoundary(query, arguments, "t", cursor)
	query += ` ORDER BY t.created_at DESC, t.id DESC LIMIT ?`
	arguments = append(arguments, limit)
	rows, err := repository.executor.QueryContext(ctx, repository.bind(query), arguments...)
	if err != nil {
		return nil, databaseError("list tasks", err)
	}
	defer rows.Close()
	tasks := make([]Task, 0)
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, databaseError("iterate tasks", err)
	}
	return tasks, nil
}

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
		return errors.New("expected state, next state and update time are required")
	}
	var err error
	if result, err = normalizeJSON(result, false, "task result"); err != nil {
		return err
	}
	query := repository.bind(`UPDATE tasks SET state = ?, result_json = ?, updated_at = ? WHERE id = ? AND state = ?`)
	if repository.backend == BackendPostgreSQL {
		query = `UPDATE tasks SET state = $1, result_json = $2::jsonb, updated_at = $3 WHERE id = $4 AND state = $5`
	}
	writeResult, err := repository.executor.ExecContext(ctx, query, nextState, nullableJSON(result), formatTime(updatedAt), id, expectedState)
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

func (repository *taskRepository) ListBySession(ctx context.Context, sessionID string, limit int) ([]Task, error) {
	if err := validateUUID(sessionID, "session ID"); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		return nil, errors.New("task list limit must be between 1 and 1000")
	}
	query := repository.bind(`SELECT id, identity_id, session_id, type, state, spec_json,
		result_json, idempotency_key, created_at, updated_at, expires_at
		FROM tasks WHERE session_id = ? ORDER BY updated_at DESC, id ASC LIMIT ?`)
	rows, err := repository.executor.QueryContext(ctx, query, sessionID, limit)
	if err != nil {
		return nil, databaseError("list session tasks", err)
	}
	defer rows.Close()
	var tasks []Task
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, databaseError("iterate session tasks", err)
	}
	if tasks == nil {
		tasks = []Task{}
	}
	return tasks, nil
}

func (repository *taskRepository) ListStaleByTypeStates(
	ctx context.Context,
	taskType string,
	states []remotetask.State,
	before time.Time,
	limit int,
) ([]Task, error) {
	taskType = strings.TrimSpace(taskType)
	if taskType == "" || len(states) == 0 || len(states) > 16 || before.IsZero() {
		return nil, errors.New("task type, states and stale boundary are required")
	}
	if limit <= 0 || limit > 1000 {
		return nil, errors.New("task list limit must be between 1 and 1000")
	}
	arguments := make([]any, 0, len(states)+3)
	arguments = append(arguments, taskType)
	placeholders := make([]string, 0, len(states))
	for _, state := range states {
		if !state.Valid() {
			return nil, errors.New("task states cannot be empty")
		}
		placeholders = append(placeholders, "?")
		arguments = append(arguments, state)
	}
	arguments = append(arguments, formatTime(before), limit)
	query := fmt.Sprintf(`SELECT id, identity_id, session_id, type, state, spec_json,
		result_json, idempotency_key, created_at, updated_at, expires_at
		FROM tasks WHERE type = ? AND state IN (%s) AND updated_at < ?
		ORDER BY updated_at ASC, id ASC LIMIT ?`, strings.Join(placeholders, ","))
	query = repository.bind(query)
	rows, err := repository.executor.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, databaseError("list stale tasks", err)
	}
	defer rows.Close()
	var tasks []Task
	for rows.Next() {
		task, scanErr := scanTask(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, databaseError("iterate stale tasks", err)
	}
	if tasks == nil {
		tasks = []Task{}
	}
	return tasks, nil
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
		return errors.New("expected state, observed time, next state and update time are required")
	}
	var err error
	if result, err = normalizeJSON(result, false, "task result"); err != nil {
		return err
	}
	query := repository.bind(`UPDATE tasks SET state = ?, result_json = ?, updated_at = ?
		WHERE id = ? AND state = ? AND updated_at = ?`)
	if repository.backend == BackendPostgreSQL {
		query = `UPDATE tasks SET state = $1, result_json = $2::jsonb, updated_at = $3
			WHERE id = $4 AND state = $5 AND updated_at = $6`
	}
	writeResult, err := repository.executor.ExecContext(
		ctx, query, nextState, nullableJSON(result), formatTime(updatedAt), id, expectedState, formatTime(observedUpdatedAt),
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

func normalizeTask(task *Task) error {
	if err := validateUUID(task.ID, "task ID"); err != nil {
		return err
	}
	if err := validateUUID(task.IdentityID, "identity ID"); err != nil {
		return err
	}
	if err := validateUUID(task.SessionID, "session ID"); err != nil {
		return err
	}
	task.Type = strings.TrimSpace(task.Type)
	task.IdempotencyKey = strings.TrimSpace(task.IdempotencyKey)
	if task.Type == "" || !task.State.Valid() || task.IdempotencyKey == "" {
		return errors.New("task type, state and idempotency key are required")
	}
	var err error
	if task.Spec, err = normalizeJSON(task.Spec, true, "task spec"); err != nil {
		return err
	}
	if task.Result, err = normalizeJSON(task.Result, false, "task result"); err != nil {
		return err
	}
	if task.CreatedAt.IsZero() {
		return errors.New("task creation time is required")
	}
	if task.UpdatedAt.IsZero() {
		task.UpdatedAt = task.CreatedAt
	}
	task.CreatedAt = task.CreatedAt.UTC()
	task.UpdatedAt = task.UpdatedAt.UTC()
	if task.ExpiresAt != nil {
		value := task.ExpiresAt.UTC()
		task.ExpiresAt = &value
	}
	return nil
}

func scanTask(row rowScanner) (Task, error) {
	var (
		task                 Task
		spec, result         []byte
		createdAt, updatedAt string
		expiresAt            sql.NullString
	)
	if err := row.Scan(
		&task.ID, &task.IdentityID, &task.SessionID, &task.Type,
		&task.State, &spec, &result, &task.IdempotencyKey, &createdAt, &updatedAt, &expiresAt,
	); err != nil {
		return Task{}, err
	}
	task.Spec = append(json.RawMessage(nil), spec...)
	if len(result) > 0 {
		task.Result = append(json.RawMessage(nil), result...)
	}
	var err error
	if task.CreatedAt, err = parseTime(createdAt, "task creation time"); err != nil {
		return Task{}, err
	}
	if task.UpdatedAt, err = parseTime(updatedAt, "task update time"); err != nil {
		return Task{}, err
	}
	if task.ExpiresAt, err = parseNullableTime(expiresAt, "task expiry"); err != nil {
		return Task{}, err
	}
	return task, nil
}
