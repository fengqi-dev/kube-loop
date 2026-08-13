package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type adminPolicyRevisionRepository struct{ repositoryBase }
type providerConfigRevisionRepository struct{ repositoryBase }

func (repository *adminPolicyRevisionRepository) Create(ctx context.Context, revision AdminPolicyRevision) (AdminPolicyRevision, error) {
	if err := normalizeAdminPolicyRevision(&revision); err != nil {
		return AdminPolicyRevision{}, err
	}
	query := repository.bind(`INSERT INTO admin_policy_revisions(
		id, schema_version, spec_json, spec_hash, validation_state, validation_json, created_by,
		created_authentication_type, reason, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING revision`)
	if repository.backend == BackendPostgreSQL {
		query = `INSERT INTO admin_policy_revisions(
			id, schema_version, spec_json, spec_hash, validation_state, validation_json, created_by,
			created_authentication_type, reason, created_at
		) VALUES ($1, $2, $3::jsonb, $4, $5, $6::jsonb, $7, $8, $9, $10) RETURNING revision`
	}
	if repository.backend == BackendMySQL {
		query = strings.TrimSuffix(query, " RETURNING revision")
		result, err := repository.executor.ExecContext(ctx, query,
			revision.ID, revision.SchemaVersion, string(revision.Spec), revision.SpecHash, revision.ValidationState,
			nullableJSON(revision.Validation), revision.CreatedBy, revision.CreatedAuthenticationType,
			revision.Reason, formatTime(revision.CreatedAt),
		)
		if err != nil {
			return AdminPolicyRevision{}, mapWriteError(err)
		}
		inserted, err := result.LastInsertId()
		if err != nil || inserted <= 0 {
			return AdminPolicyRevision{}, errors.New("read policy revision")
		}
		revision.Revision = uint64(inserted)
		return revision, nil
	}
	err := repository.executor.QueryRowContext(ctx, query,
		revision.ID, revision.SchemaVersion, string(revision.Spec), revision.SpecHash, revision.ValidationState,
		nullableJSON(revision.Validation), revision.CreatedBy, revision.CreatedAuthenticationType,
		revision.Reason, formatTime(revision.CreatedAt),
	).Scan(&revision.Revision)
	if err != nil {
		return AdminPolicyRevision{}, mapWriteError(err)
	}
	return revision, nil
}

func (repository *adminPolicyRevisionRepository) Get(ctx context.Context, number uint64) (AdminPolicyRevision, error) {
	if number == 0 {
		return AdminPolicyRevision{}, errors.New("policy revision must be positive")
	}
	query := repository.bind(`SELECT revision, id, schema_version, spec_json, spec_hash, validation_state,
		validation_json, created_by, created_authentication_type, reason, created_at
		FROM admin_policy_revisions WHERE revision = ?`)
	revision, err := scanAdminPolicyRevision(repository.executor.QueryRowContext(ctx, query, number))
	if errors.Is(err, sql.ErrNoRows) {
		return AdminPolicyRevision{}, ErrNotFound
	}
	if err != nil {
		return AdminPolicyRevision{}, databaseError("read policy revision", err)
	}
	return revision, nil
}

func (repository *providerConfigRevisionRepository) Create(ctx context.Context, revision ProviderConfigRevision) (ProviderConfigRevision, error) {
	if err := normalizeProviderConfigRevision(&revision); err != nil {
		return ProviderConfigRevision{}, err
	}
	query := repository.bind(`INSERT INTO provider_config_revisions(
		id, schema_version, provider_id, provider_type, config_json, config_hash, secret_aliases_json,
		validation_state, validation_json, created_by, created_authentication_type, reason, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING revision`)
	if repository.backend == BackendPostgreSQL {
		query = `INSERT INTO provider_config_revisions(
			id, schema_version, provider_id, provider_type, config_json, config_hash, secret_aliases_json,
			validation_state, validation_json, created_by, created_authentication_type, reason, created_at
		) VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7::jsonb, $8, $9::jsonb, $10, $11, $12, $13) RETURNING revision`
	}
	if repository.backend == BackendMySQL {
		query = strings.TrimSuffix(query, " RETURNING revision")
		result, err := repository.executor.ExecContext(ctx, query,
			revision.ID, revision.SchemaVersion, revision.ProviderID, revision.ProviderType, string(revision.Config),
			revision.ConfigHash, string(revision.SecretAliases), revision.ValidationState, nullableJSON(revision.Validation),
			revision.CreatedBy, revision.CreatedAuthenticationType, revision.Reason, formatTime(revision.CreatedAt),
		)
		if err != nil {
			return ProviderConfigRevision{}, mapWriteError(err)
		}
		inserted, err := result.LastInsertId()
		if err != nil || inserted <= 0 {
			return ProviderConfigRevision{}, errors.New("read provider revision")
		}
		revision.Revision = uint64(inserted)
		return revision, nil
	}
	err := repository.executor.QueryRowContext(ctx, query,
		revision.ID, revision.SchemaVersion, revision.ProviderID, revision.ProviderType, string(revision.Config),
		revision.ConfigHash, string(revision.SecretAliases), revision.ValidationState, nullableJSON(revision.Validation),
		revision.CreatedBy, revision.CreatedAuthenticationType, revision.Reason, formatTime(revision.CreatedAt),
	).Scan(&revision.Revision)
	if err != nil {
		return ProviderConfigRevision{}, mapWriteError(err)
	}
	return revision, nil
}

func (repository *providerConfigRevisionRepository) Get(ctx context.Context, number uint64) (ProviderConfigRevision, error) {
	if number == 0 {
		return ProviderConfigRevision{}, errors.New("provider revision must be positive")
	}
	query := repository.bind(`SELECT revision, id, schema_version, provider_id, provider_type, config_json,
		config_hash, secret_aliases_json, validation_state, validation_json, created_by,
		created_authentication_type, reason, created_at
		FROM provider_config_revisions WHERE revision = ?`)
	revision, err := scanProviderConfigRevision(repository.executor.QueryRowContext(ctx, query, number))
	if errors.Is(err, sql.ErrNoRows) {
		return ProviderConfigRevision{}, ErrNotFound
	}
	if err != nil {
		return ProviderConfigRevision{}, databaseError("read provider configuration revision", err)
	}
	return revision, nil
}

func normalizeAdminPolicyRevision(revision *AdminPolicyRevision) error {
	if revision.Revision != 0 || validateUUID(revision.ID, "policy revision ID") != nil {
		return errors.New("policy revision identity is invalid")
	}
	var err error
	if revision.CreatedBy, revision.CreatedAuthenticationType, err = normalizeManagementActor(
		revision.CreatedBy, revision.CreatedAuthenticationType,
	); err != nil {
		return err
	}
	if revision.Spec, err = canonicalJSONObject(revision.Spec, "policy revision spec"); err != nil {
		return err
	}
	revision.SpecHash = jsonSHA256(revision.Spec)
	revision.ValidationState = strings.TrimSpace(revision.ValidationState)
	if !validRevisionState(revision.ValidationState) {
		return errors.New("policy revision validation state is invalid")
	}
	if revision.Validation, err = normalizeJSON(revision.Validation, false, "policy revision validation"); err != nil {
		return err
	}
	if revision.Reason, err = normalizeReason(revision.Reason); err != nil {
		return err
	}
	return normalizeRevisionMetadata(&revision.SchemaVersion, &revision.CreatedAt)
}

func normalizeProviderConfigRevision(revision *ProviderConfigRevision) error {
	if revision.Revision != 0 || validateUUID(revision.ID, "provider revision ID") != nil {
		return errors.New("provider revision identity is invalid")
	}
	var err error
	if revision.CreatedBy, revision.CreatedAuthenticationType, err = normalizeManagementActor(
		revision.CreatedBy, revision.CreatedAuthenticationType,
	); err != nil {
		return err
	}
	if revision.ProviderID, err = normalizeManagementIdentifier(revision.ProviderID, "provider ID"); err != nil {
		return err
	}
	revision.ProviderType = strings.TrimSpace(revision.ProviderType)
	if revision.ProviderType != "oidc" {
		return errors.New("provider type must be oidc")
	}
	if revision.Config, err = canonicalJSONObject(revision.Config, "provider configuration"); err != nil {
		return err
	}
	if err := rejectPlaintextSecrets(revision.Config); err != nil {
		return err
	}
	revision.ConfigHash = jsonSHA256(revision.Config)
	if revision.SecretAliases, err = normalizeSecretAliases(revision.SecretAliases); err != nil {
		return err
	}
	revision.ValidationState = strings.TrimSpace(revision.ValidationState)
	if !validRevisionState(revision.ValidationState) {
		return errors.New("provider revision validation state is invalid")
	}
	if revision.Validation, err = normalizeJSON(revision.Validation, false, "provider revision validation"); err != nil {
		return err
	}
	if revision.Reason, err = normalizeReason(revision.Reason); err != nil {
		return err
	}
	return normalizeRevisionMetadata(&revision.SchemaVersion, &revision.CreatedAt)
}

func normalizeRevisionMetadata(schemaVersion *int, createdAt *time.Time) error {
	if *schemaVersion == 0 {
		*schemaVersion = ObjectSchemaVersion
	}
	if *schemaVersion != ObjectSchemaVersion || createdAt.IsZero() {
		return errors.New("management revision schema or creation time is invalid")
	}
	*createdAt = createdAt.UTC()
	return nil
}

func scanAdminPolicyRevision(row rowScanner) (AdminPolicyRevision, error) {
	var revision AdminPolicyRevision
	var spec, validation []byte
	var createdAt string
	if err := row.Scan(&revision.Revision, &revision.ID, &revision.SchemaVersion, &spec, &revision.SpecHash,
		&revision.ValidationState, &validation, &revision.CreatedBy, &revision.CreatedAuthenticationType,
		&revision.Reason, &createdAt); err != nil {
		return AdminPolicyRevision{}, err
	}
	revision.Spec = append(json.RawMessage(nil), spec...)
	var err error
	if revision.Spec, err = canonicalJSONObject(revision.Spec, "policy revision spec"); err != nil {
		return AdminPolicyRevision{}, err
	}
	if len(validation) > 0 {
		revision.Validation = append(json.RawMessage(nil), validation...)
	}
	if revision.CreatedAt, err = parseTime(createdAt, "policy revision creation time"); err != nil {
		return AdminPolicyRevision{}, err
	}
	return revision, nil
}

func scanProviderConfigRevision(row rowScanner) (ProviderConfigRevision, error) {
	var revision ProviderConfigRevision
	var config, aliases, validation []byte
	var createdAt string
	if err := row.Scan(&revision.Revision, &revision.ID, &revision.SchemaVersion, &revision.ProviderID,
		&revision.ProviderType, &config, &revision.ConfigHash, &aliases, &revision.ValidationState,
		&validation, &revision.CreatedBy, &revision.CreatedAuthenticationType, &revision.Reason, &createdAt); err != nil {
		return ProviderConfigRevision{}, err
	}
	revision.Config = append(json.RawMessage(nil), config...)
	revision.SecretAliases = append(json.RawMessage(nil), aliases...)
	var err error
	if revision.Config, err = canonicalJSONObject(revision.Config, "provider configuration"); err != nil {
		return ProviderConfigRevision{}, err
	}
	if revision.SecretAliases, err = normalizeSecretAliases(revision.SecretAliases); err != nil {
		return ProviderConfigRevision{}, err
	}
	if len(validation) > 0 {
		revision.Validation = append(json.RawMessage(nil), validation...)
	}
	if revision.CreatedAt, err = parseTime(createdAt, "provider revision creation time"); err != nil {
		return ProviderConfigRevision{}, err
	}
	return revision, nil
}
