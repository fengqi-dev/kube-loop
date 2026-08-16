package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const maximumAuditExportBytes = 4 << 20

type auditExportJobRepository struct{ repositoryBase }

func (repository *auditExportJobRepository) Create(ctx context.Context, job AuditExportJob) error {
	if err := normalizeAuditExportJob(&job); err != nil {
		return err
	}
	query := repository.bind(`INSERT INTO audit_export_jobs(id, state, filter_json, result_data,
		error_code, requested_by, requested_authentication_type, reason, created_at, updated_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err := repository.executor.ExecContext(ctx, query, job.ID, job.State, string(job.Filter),
		job.Result, job.ErrorCode, job.RequestedBy, job.RequestedAuthenticationType, job.Reason,
		formatTime(job.CreatedAt), formatTime(job.UpdatedAt), formatTime(job.ExpiresAt))
	return mapWriteError(err)
}

func (repository *auditExportJobRepository) GetByID(ctx context.Context, id string) (AuditExportJob, error) {
	if err := validateUUID(id, "audit export job ID"); err != nil {
		return AuditExportJob{}, err
	}
	query := repository.bind(`SELECT id, state, filter_json, result_data, error_code,
		requested_by, requested_authentication_type, reason, created_at, updated_at, expires_at
		FROM audit_export_jobs WHERE id = ?`)
	job, err := scanAuditExportJob(repository.executor.QueryRowContext(ctx, query, id))
	if errors.Is(err, sql.ErrNoRows) {
		return AuditExportJob{}, ErrNotFound
	}
	if err != nil {
		return AuditExportJob{}, databaseError("read audit export job", err)
	}
	return job, nil
}

func (repository *auditExportJobRepository) ListRunnable(ctx context.Context, staleBefore time.Time, limit int) ([]AuditExportJob, error) {
	limit, err := boundedLimit(limit)
	if err != nil {
		return nil, err
	}
	if staleBefore.IsZero() {
		return nil, errors.New("audit export stale boundary is required")
	}
	query := repository.bind(`SELECT id, state, filter_json, result_data, error_code,
		requested_by, requested_authentication_type, reason, created_at, updated_at, expires_at
		FROM audit_export_jobs WHERE state = 'pending' OR (state = 'running' AND updated_at < ?)
		ORDER BY created_at, id LIMIT ?`)
	rows, err := repository.executor.QueryContext(ctx, query, formatTime(staleBefore), limit)
	if err != nil {
		return nil, databaseError("list pending audit export jobs", err)
	}
	defer rows.Close()
	jobs := make([]AuditExportJob, 0)
	for rows.Next() {
		job, scanErr := scanAuditExportJob(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (repository *auditExportJobRepository) Claim(ctx context.Context, id string, observed, staleBefore, now time.Time) error {
	if validateUUID(id, "audit export job ID") != nil || observed.IsZero() || staleBefore.IsZero() || now.IsZero() {
		return errors.New("audit export claim values are invalid")
	}
	query := repository.bind(`UPDATE audit_export_jobs SET state = 'running', updated_at = ?
		WHERE id = ? AND updated_at = ? AND (state = 'pending' OR (state = 'running' AND updated_at < ?))`)
	result, err := repository.executor.ExecContext(ctx, query, formatTime(now), id, formatTime(observed), formatTime(staleBefore))
	if err != nil {
		return mapWriteError(err)
	}
	count, err := rowsAffected(result)
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}

func (repository *auditExportJobRepository) Complete(ctx context.Context, id, state, resultData, errorCode string, now time.Time) error {
	if validateUUID(id, "audit export job ID") != nil || (state != "succeeded" && state != "failed") || now.IsZero() ||
		len(resultData) > maximumAuditExportBytes || strings.ContainsAny(errorCode, "\x00\r\n") {
		return errors.New("audit export completion values are invalid")
	}
	if state == "succeeded" && (resultData == "" || errorCode != "") || state == "failed" && (resultData != "" || errorCode == "") {
		return errors.New("audit export completion outcome is inconsistent")
	}
	query := repository.bind(`UPDATE audit_export_jobs SET state = ?, result_data = ?, error_code = ?, updated_at = ?
		WHERE id = ? AND state = 'running'`)
	result, err := repository.executor.ExecContext(ctx, query, state, resultData, errorCode, formatTime(now), id)
	if err != nil {
		return mapWriteError(err)
	}
	count, err := rowsAffected(result)
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}

func normalizeAuditExportJob(job *AuditExportJob) error {
	if err := validateUUID(job.ID, "audit export job ID"); err != nil {
		return err
	}
	job.RequestedBy = strings.TrimSpace(job.RequestedBy)
	job.RequestedAuthenticationType = strings.TrimSpace(job.RequestedAuthenticationType)
	job.Reason = strings.TrimSpace(job.Reason)
	if job.State != "pending" || !json.Valid(job.Filter) || job.Result != "" || job.ErrorCode != "" ||
		job.RequestedBy == "" || job.Reason == "" || job.CreatedAt.IsZero() || job.UpdatedAt.IsZero() || !job.ExpiresAt.After(job.CreatedAt) {
		return errors.New("audit export job is invalid")
	}
	switch job.RequestedAuthenticationType {
	case "normal", "bootstrap":
	default:
		return errors.New("audit export authentication type is invalid")
	}
	job.CreatedAt, job.UpdatedAt, job.ExpiresAt = job.CreatedAt.UTC(), job.UpdatedAt.UTC(), job.ExpiresAt.UTC()
	return nil
}

func scanAuditExportJob(row rowScanner) (AuditExportJob, error) {
	var job AuditExportJob
	var filter []byte
	var createdAt, updatedAt, expiresAt string
	if err := row.Scan(&job.ID, &job.State, &filter, &job.Result, &job.ErrorCode,
		&job.RequestedBy, &job.RequestedAuthenticationType, &job.Reason, &createdAt, &updatedAt, &expiresAt); err != nil {
		return AuditExportJob{}, err
	}
	job.Filter = append(json.RawMessage(nil), filter...)
	var err error
	if job.CreatedAt, err = parseTime(createdAt, "audit export creation time"); err != nil {
		return AuditExportJob{}, err
	}
	if job.UpdatedAt, err = parseTime(updatedAt, "audit export update time"); err != nil {
		return AuditExportJob{}, err
	}
	job.ExpiresAt, err = parseTime(expiresAt, "audit export expiry")
	return job, err
}
