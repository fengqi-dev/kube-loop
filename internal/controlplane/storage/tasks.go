package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
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
	_, err := repository.executor.ExecContext(
		ctx,
		query,
		task.ID,
		task.IdentityID,
		task.SessionID,
		task.Type,
		task.State,
		string(task.Spec),
		nullableJSON(task.Result),
		task.IdempotencyKey,
		formatTime(
			task.CreatedAt,
		),
		formatTime(task.UpdatedAt),
		nullableTime(task.ExpiresAt),
	)
	return mapWriteError(err)
}

func (repository *taskRepository) GetByID(
	ctx context.Context,
	id string,
) (Task, error) {
	if err := validateUUID(id, "task ID"); err != nil {
		return Task{}, err
	}
	query := repository.bind(
		`SELECT id, identity_id, session_id, type, state, spec_json,
		result_json, idempotency_key, created_at, updated_at, expires_at FROM tasks WHERE id = ?`,
	)
	task, err := scanTask(repository.executor.QueryRowContext(ctx, query, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	if err != nil {
		return Task{}, databaseError("read task", err)
	}
	return task, nil
}

func (repository *taskRepository) List(
	ctx context.Context,
	filter TaskListFilter,
) ([]Task, error) {
	limit, cursor, err := normalizePage(filter.Limit, filter.Cursor)
	if err != nil {
		return nil, err
	}
	filter.IdentityID = strings.TrimSpace(filter.IdentityID)
	filter.SessionID = strings.TrimSpace(filter.SessionID)
	filter.Namespace = strings.TrimSpace(filter.Namespace)
	filter.Type = strings.TrimSpace(filter.Type)
	if filter.IdentityID != "" &&
		validateUUID(filter.IdentityID, "task identity ID") != nil {
		return nil, errors.New("task identity filter is invalid")
	}
	if filter.SessionID != "" &&
		validateUUID(filter.SessionID, "task session ID") != nil {
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
	rows, err := repository.executor.QueryContext(
		ctx,
		repository.bind(query),
		arguments...)
	if err != nil {
		return nil, databaseError("list tasks", err)
	}
	defer func() { _ = rows.Close() }()
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
