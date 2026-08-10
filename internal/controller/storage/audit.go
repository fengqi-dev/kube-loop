package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type auditRepository struct {
	repositoryBase
}

func (repository *auditRepository) Append(ctx context.Context, event AuditEvent) error {
	if err := normalizeAuditEvent(&event); err != nil {
		return err
	}
	query := repository.bind(`INSERT INTO audit_events(
		id, schema_version, principal_id, action, resource_type, resource_id,
		outcome, request_id, metadata_json, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if repository.backend == BackendPostgreSQL {
		query = `INSERT INTO audit_events(
			id, schema_version, principal_id, action, resource_type, resource_id,
			outcome, request_id, metadata_json, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb, $10)`
	}
	var principalID any
	if event.PrincipalID != "" {
		principalID = event.PrincipalID
	}
	_, err := repository.executor.ExecContext(ctx, query,
		event.ID, event.SchemaVersion, principalID, event.Action, event.ResourceType,
		event.ResourceID, event.Outcome, event.RequestID, nullableJSON(event.Metadata), formatTime(event.CreatedAt),
	)
	return mapWriteError(err)
}

func (repository *auditRepository) List(ctx context.Context, filter AuditFilter) ([]AuditEvent, error) {
	limit := filter.Limit
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > 1000 {
		return nil, errors.New("audit list limit must be between 1 and 1000")
	}
	query := `SELECT id, schema_version, principal_id, action, resource_type, resource_id,
		outcome, request_id, metadata_json, created_at FROM audit_events WHERE 1=1`
	var arguments []any
	if filter.PrincipalID != "" {
		if err := validateUUID(filter.PrincipalID, "principal ID"); err != nil {
			return nil, err
		}
		query += ` AND principal_id = ?`
		arguments = append(arguments, filter.PrincipalID)
	}
	if filter.Action != "" {
		query += ` AND action = ?`
		arguments = append(arguments, strings.TrimSpace(filter.Action))
	}
	if !filter.After.IsZero() {
		query += ` AND created_at >= ?`
		arguments = append(arguments, formatTime(filter.After))
	}
	if !filter.Before.IsZero() {
		query += ` AND created_at < ?`
		arguments = append(arguments, formatTime(filter.Before))
	}
	query += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	arguments = append(arguments, limit)
	rows, err := repository.executor.QueryContext(ctx, repository.bind(query), arguments...)
	if err != nil {
		return nil, databaseError("list audit events", err)
	}
	defer rows.Close()
	var events []AuditEvent
	for rows.Next() {
		event, err := scanAuditEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, databaseError("iterate audit events", err)
	}
	if events == nil {
		events = []AuditEvent{}
	}
	return events, nil
}

func normalizeAuditEvent(event *AuditEvent) error {
	if err := validateUUID(event.ID, "audit event ID"); err != nil {
		return err
	}
	if event.PrincipalID != "" {
		if err := validateUUID(event.PrincipalID, "principal ID"); err != nil {
			return err
		}
	}
	event.Action = strings.TrimSpace(event.Action)
	event.ResourceType = strings.TrimSpace(event.ResourceType)
	event.ResourceID = strings.TrimSpace(event.ResourceID)
	event.Outcome = strings.TrimSpace(event.Outcome)
	event.RequestID = strings.TrimSpace(event.RequestID)
	if event.Action == "" || event.Outcome == "" || event.RequestID == "" {
		return errors.New("audit action, outcome and request ID are required")
	}
	var err error
	if event.Metadata, err = normalizeJSON(event.Metadata, false, "audit metadata"); err != nil {
		return err
	}
	if event.SchemaVersion == 0 {
		event.SchemaVersion = ObjectSchemaVersion
	}
	if event.SchemaVersion != ObjectSchemaVersion {
		return errors.New("unsupported audit schema version")
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	event.CreatedAt = event.CreatedAt.UTC()
	return nil
}

func scanAuditEvent(row rowScanner) (AuditEvent, error) {
	var (
		event       AuditEvent
		principalID sql.NullString
		metadata    []byte
		createdAt   string
	)
	if err := row.Scan(
		&event.ID, &event.SchemaVersion, &principalID, &event.Action, &event.ResourceType,
		&event.ResourceID, &event.Outcome, &event.RequestID, &metadata, &createdAt,
	); err != nil {
		return AuditEvent{}, err
	}
	if principalID.Valid {
		event.PrincipalID = principalID.String
	}
	if len(metadata) > 0 {
		event.Metadata = append(json.RawMessage(nil), metadata...)
	}
	var err error
	if event.CreatedAt, err = parseTime(createdAt, "audit creation time"); err != nil {
		return AuditEvent{}, err
	}
	return event, nil
}
