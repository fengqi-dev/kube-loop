package storage

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type resourceSnapshotRepository struct {
	repositoryBase
}

func (repository *resourceSnapshotRepository) Put(ctx context.Context, snapshot ResourceSnapshot) error {
	if err := normalizeResourceSnapshot(&snapshot); err != nil {
		return err
	}
	query := repository.bind(`INSERT INTO resource_snapshots(
		id, schema_version, task_id, kind, namespace, name, data_json, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(task_id, kind, namespace, name) DO UPDATE SET
		schema_version=excluded.schema_version, data_json=excluded.data_json, created_at=excluded.created_at`)
	if repository.backend == BackendPostgreSQL {
		query = `INSERT INTO resource_snapshots(
			id, schema_version, task_id, kind, namespace, name, data_json, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8)
		ON CONFLICT(task_id, kind, namespace, name) DO UPDATE SET
			schema_version=excluded.schema_version, data_json=excluded.data_json, created_at=excluded.created_at`
	}
	if repository.backend == BackendMySQL {
		query = `INSERT INTO resource_snapshots(
			id, schema_version, task_id, kind, namespace, name, data_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE schema_version=VALUES(schema_version),
			data_json=VALUES(data_json), created_at=VALUES(created_at)`
	}
	_, err := repository.executor.ExecContext(ctx, query,
		snapshot.ID, snapshot.SchemaVersion, snapshot.TaskID, snapshot.Kind,
		snapshot.Namespace, snapshot.Name, string(snapshot.Data), formatTime(snapshot.CreatedAt),
	)
	return mapWriteError(err)
}

func (repository *resourceSnapshotRepository) ListByTask(ctx context.Context, taskID string) ([]ResourceSnapshot, error) {
	if err := validateUUID(taskID, "task ID"); err != nil {
		return nil, err
	}
	query := repository.bind(`SELECT id, schema_version, task_id, kind, namespace, name, data_json, created_at
		FROM resource_snapshots WHERE task_id = ? ORDER BY kind, namespace, name`)
	rows, err := repository.executor.QueryContext(ctx, query, taskID)
	if err != nil {
		return nil, databaseError("list resource snapshots", err)
	}
	defer rows.Close()
	var snapshots []ResourceSnapshot
	for rows.Next() {
		var snapshot ResourceSnapshot
		var data []byte
		var createdAt string
		if err := rows.Scan(
			&snapshot.ID, &snapshot.SchemaVersion, &snapshot.TaskID, &snapshot.Kind,
			&snapshot.Namespace, &snapshot.Name, &data, &createdAt,
		); err != nil {
			return nil, errors.New("decode resource snapshot")
		}
		snapshot.Data = append(json.RawMessage(nil), data...)
		if snapshot.CreatedAt, err = parseTime(createdAt, "resource snapshot creation time"); err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}
	if err := rows.Err(); err != nil {
		return nil, databaseError("iterate resource snapshots", err)
	}
	if snapshots == nil {
		snapshots = []ResourceSnapshot{}
	}
	return snapshots, nil
}

func (repository *resourceSnapshotRepository) DeleteByTask(ctx context.Context, taskID string) (int64, error) {
	if err := validateUUID(taskID, "task ID"); err != nil {
		return 0, err
	}
	result, err := repository.executor.ExecContext(ctx, repository.bind(`DELETE FROM resource_snapshots WHERE task_id = ?`), taskID)
	if err != nil {
		return 0, databaseError("delete resource snapshots", err)
	}
	return rowsAffected(result)
}

func normalizeResourceSnapshot(snapshot *ResourceSnapshot) error {
	if err := validateUUID(snapshot.ID, "resource snapshot ID"); err != nil {
		return err
	}
	if err := validateUUID(snapshot.TaskID, "task ID"); err != nil {
		return err
	}
	snapshot.Kind = strings.TrimSpace(snapshot.Kind)
	snapshot.Namespace = strings.TrimSpace(snapshot.Namespace)
	snapshot.Name = strings.TrimSpace(snapshot.Name)
	if snapshot.Kind == "" || snapshot.Name == "" {
		return errors.New("resource snapshot kind and name are required")
	}
	var err error
	if snapshot.Data, err = normalizeJSON(snapshot.Data, true, "resource snapshot data"); err != nil {
		return err
	}
	if snapshot.SchemaVersion == 0 {
		snapshot.SchemaVersion = ObjectSchemaVersion
	}
	if snapshot.SchemaVersion != ObjectSchemaVersion {
		return errors.New("unsupported resource snapshot schema version")
	}
	if snapshot.CreatedAt.IsZero() {
		snapshot.CreatedAt = time.Now().UTC()
	}
	snapshot.CreatedAt = snapshot.CreatedAt.UTC()
	return nil
}
