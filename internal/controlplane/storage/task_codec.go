package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

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
