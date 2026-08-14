package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type configChangeRequestRepository struct{ repositoryBase }

func (repository *configChangeRequestRepository) Create(ctx context.Context, request ConfigChangeRequest) error {
	if err := normalizeConfigChangeRequest(&request); err != nil {
		return err
	}
	query := repository.bind(`INSERT INTO config_change_requests(
		id, schema_version, configuration_type, configuration_id, base_object_id, proposed_object_id,
		status, idempotency_hash, request_hash, requested_by, requested_authentication_type,
		reason, validation_json, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if repository.backend == BackendPostgreSQL {
		query = `INSERT INTO config_change_requests(
			id, schema_version, configuration_type, configuration_id, base_object_id, proposed_object_id,
			status, idempotency_hash, request_hash, requested_by, requested_authentication_type,
			reason, validation_json, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13::jsonb, $14, $15)`
	}
	_, err := repository.executor.ExecContext(ctx, query,
		request.ID, request.SchemaVersion, request.ConfigurationType, request.ConfigurationID,
		nullableString(request.BaseObjectID), request.ProposedObjectID, request.Status,
		request.IdempotencyHash, request.RequestHash, request.RequestedBy, request.RequestedAuthenticationType,
		request.Reason, nullableJSON(request.Validation), formatTime(request.CreatedAt), formatTime(request.UpdatedAt),
	)
	return mapWriteError(err)
}

const changeSelect = `SELECT id, schema_version, configuration_type, configuration_id, base_object_id,
	proposed_object_id, status, idempotency_hash, request_hash, requested_by, requested_authentication_type,
	reason, validation_json, created_at, updated_at FROM config_change_requests`

func (repository *configChangeRequestRepository) GetByID(ctx context.Context, id string) (ConfigChangeRequest, error) {
	if validateUUID(strings.TrimSpace(id), "configuration change request ID") != nil {
		return ConfigChangeRequest{}, errors.New("configuration change request ID is invalid")
	}
	request, err := scanConfigChangeRequest(repository.executor.QueryRowContext(ctx, repository.bind(changeSelect+` WHERE id = ?`), id))
	if errors.Is(err, sql.ErrNoRows) {
		return ConfigChangeRequest{}, ErrNotFound
	}
	if err != nil {
		return ConfigChangeRequest{}, databaseError("read configuration change request", err)
	}
	return request, nil
}

func (repository *configChangeRequestRepository) GetByIdempotencyHash(
	ctx context.Context, requestedBy, requestedAuthenticationType, kind, configurationID string, hash []byte,
) (ConfigChangeRequest, error) {
	var err error
	if requestedBy, requestedAuthenticationType, err = normalizeManagementActor(requestedBy, requestedAuthenticationType); err != nil || len(hash) != sha256Size {
		return ConfigChangeRequest{}, errors.New("configuration change idempotency identity is invalid")
	}
	kind, configurationID, err = normalizeConfigurationIdentity(kind, configurationID)
	if err != nil {
		return ConfigChangeRequest{}, err
	}
	query := repository.bind(changeSelect + ` WHERE requested_by = ? AND requested_authentication_type = ?
		AND configuration_type = ? AND configuration_id = ? AND idempotency_hash = ?`)
	request, err := scanConfigChangeRequest(repository.executor.QueryRowContext(
		ctx, query, requestedBy, requestedAuthenticationType, kind, configurationID, hash,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return ConfigChangeRequest{}, ErrNotFound
	}
	if err != nil {
		return ConfigChangeRequest{}, databaseError("read idempotent configuration change request", err)
	}
	return request, nil
}

func (repository *configChangeRequestRepository) UpdateStatus(
	ctx context.Context, id, expectedStatus, nextStatus string, validation json.RawMessage, updatedAt time.Time,
) error {
	id, expectedStatus, nextStatus = strings.TrimSpace(id), strings.TrimSpace(expectedStatus), strings.TrimSpace(nextStatus)
	if validateUUID(id, "configuration change request ID") != nil || !validChangeTransition(expectedStatus, nextStatus) || updatedAt.IsZero() {
		return errors.New("configuration change request transition is invalid")
	}
	var err error
	if validation, err = normalizeJSON(validation, false, "configuration change validation"); err != nil {
		return err
	}
	query := repository.bind(`UPDATE config_change_requests SET status = ?, validation_json = ?, updated_at = ?
		WHERE id = ? AND status = ? AND updated_at <= ?`)
	if repository.backend == BackendPostgreSQL {
		query = `UPDATE config_change_requests SET status = $1, validation_json = $2::jsonb, updated_at = $3
			WHERE id = $4 AND status = $5 AND updated_at <= $6`
	}
	result, err := repository.executor.ExecContext(ctx, query, nextStatus, nullableJSON(validation),
		formatTime(updatedAt), id, expectedStatus, formatTime(updatedAt))
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

func normalizeConfigChangeRequest(request *ConfigChangeRequest) error {
	if validateUUID(request.ID, "configuration change request ID") != nil ||
		validateUUID(request.ProposedObjectID, "proposed configuration object ID") != nil ||
		len(request.IdempotencyHash) != sha256Size || !validSHA256Hex(request.RequestHash) {
		return errors.New("configuration change request identity is invalid")
	}
	if request.BaseObjectID != "" && validateUUID(request.BaseObjectID, "base configuration object ID") != nil {
		return errors.New("configuration change request base is invalid")
	}
	var err error
	if request.RequestedBy, request.RequestedAuthenticationType, err = normalizeManagementActor(
		request.RequestedBy, request.RequestedAuthenticationType,
	); err != nil {
		return err
	}
	if request.ConfigurationType, request.ConfigurationID, err = normalizeConfigurationIdentity(
		request.ConfigurationType, request.ConfigurationID,
	); err != nil {
		return err
	}
	request.Status = strings.TrimSpace(request.Status)
	if request.Status != ChangeStatusDraft && request.Status != ChangeStatusValidated {
		return errors.New("new configuration change request must be draft or validated")
	}
	if request.Reason, err = normalizeReason(request.Reason); err != nil {
		return err
	}
	if request.Validation, err = normalizeJSON(request.Validation, false, "configuration change validation"); err != nil {
		return err
	}
	if request.SchemaVersion == 0 {
		request.SchemaVersion = ObjectSchemaVersion
	}
	if request.SchemaVersion != ObjectSchemaVersion || request.CreatedAt.IsZero() || request.UpdatedAt.IsZero() || request.UpdatedAt.Before(request.CreatedAt) {
		return errors.New("configuration change request schema or timestamps are invalid")
	}
	request.IdempotencyHash = append([]byte(nil), request.IdempotencyHash...)
	request.CreatedAt, request.UpdatedAt = request.CreatedAt.UTC(), request.UpdatedAt.UTC()
	return nil
}

func scanConfigChangeRequest(row rowScanner) (ConfigChangeRequest, error) {
	var request ConfigChangeRequest
	var baseObjectID sql.NullString
	var validation []byte
	var createdAt, updatedAt string
	if err := row.Scan(&request.ID, &request.SchemaVersion, &request.ConfigurationType, &request.ConfigurationID,
		&baseObjectID, &request.ProposedObjectID, &request.Status, &request.IdempotencyHash,
		&request.RequestHash, &request.RequestedBy, &request.RequestedAuthenticationType, &request.Reason,
		&validation, &createdAt, &updatedAt); err != nil {
		return ConfigChangeRequest{}, err
	}
	if baseObjectID.Valid {
		request.BaseObjectID = baseObjectID.String
	}
	if len(validation) > 0 {
		request.Validation = append(json.RawMessage(nil), validation...)
	}
	var err error
	if request.CreatedAt, err = parseTime(createdAt, "configuration change creation time"); err != nil {
		return ConfigChangeRequest{}, err
	}
	if request.UpdatedAt, err = parseTime(updatedAt, "configuration change update time"); err != nil {
		return ConfigChangeRequest{}, err
	}
	return request, nil
}
