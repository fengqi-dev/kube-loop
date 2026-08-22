package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

func (repository *taskRepository) ListBySession(
	ctx context.Context,
	sessionID string,
	limit int,
) ([]Task, error) {
	if err := validateUUID(sessionID, "session ID"); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		return nil, errors.New("task list limit must be between 1 and 1000")
	}
	query := repository.bind(
		`SELECT id, identity_id, session_id, type, state, spec_json,
		result_json, idempotency_key, created_at, updated_at, expires_at
		FROM tasks WHERE session_id = ? ORDER BY updated_at DESC, id ASC LIMIT ?`,
	)
	rows, err := repository.executor.QueryContext(ctx, query, sessionID, limit)
	if err != nil {
		return nil, databaseError("list session tasks", err)
	}
	defer func() { _ = rows.Close() }()
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
	if taskType == "" || len(states) == 0 || len(states) > 16 ||
		before.IsZero() {
		return nil, errors.New(
			"task type, states and stale boundary are required",
		)
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
	query := fmt.Sprintf(
		`SELECT id, identity_id, session_id, type, state, spec_json,
		result_json, idempotency_key, created_at, updated_at, expires_at
		FROM tasks WHERE type = ? AND state IN (%s) AND updated_at < ?
		ORDER BY updated_at ASC, id ASC LIMIT ?`,
		strings.Join(placeholders, ","),
	)
	query = repository.bind(query)
	rows, err := repository.executor.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, databaseError("list stale tasks", err)
	}
	defer func() { _ = rows.Close() }()
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
