package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type adminPolicyConfigRepository struct{ repositoryBase }
type providerConfigRepository struct{ repositoryBase }

func (repository *adminPolicyConfigRepository) Create(ctx context.Context, config AdminPolicyConfig) (AdminPolicyConfig, error) {
	if err := normalizeAdminPolicyConfig(&config); err != nil {
		return AdminPolicyConfig{}, err
	}
	query := repository.bind(`INSERT INTO authorization_policies(
		id, schema_version, spec_json, spec_hash, validation_state, validation_json,
		created_by, created_authentication_type, reason, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if repository.backend == BackendPostgreSQL {
		query = `INSERT INTO authorization_policies(
			id, schema_version, spec_json, spec_hash, validation_state, validation_json,
			created_by, created_authentication_type, reason, created_at
		) VALUES ($1, $2, $3::jsonb, $4, $5, $6::jsonb, $7, $8, $9, $10)`
	}
	_, err := repository.executor.ExecContext(ctx, query,
		config.ID, config.SchemaVersion, string(config.Spec), config.SpecHash, config.ValidationState,
		nullableJSON(config.Validation), config.CreatedBy, config.CreatedAuthenticationType,
		config.Reason, formatTime(config.CreatedAt),
	)
	if err != nil {
		return AdminPolicyConfig{}, mapWriteError(err)
	}
	return config, nil
}

func (repository *adminPolicyConfigRepository) Get(ctx context.Context, id string) (AdminPolicyConfig, error) {
	if validateUUID(strings.TrimSpace(id), "policy configuration ID") != nil {
		return AdminPolicyConfig{}, errors.New("policy configuration ID is invalid")
	}
	query := repository.bind(`SELECT id, schema_version, spec_json, spec_hash, validation_state,
		validation_json, created_by, created_authentication_type, reason, created_at
		FROM authorization_policies WHERE id = ?`)
	config, err := scanAdminPolicyConfig(repository.executor.QueryRowContext(ctx, query, id))
	if errors.Is(err, sql.ErrNoRows) {
		return AdminPolicyConfig{}, ErrNotFound
	}
	if err != nil {
		return AdminPolicyConfig{}, databaseError("read policy configuration", err)
	}
	return config, nil
}

func (repository *providerConfigRepository) Create(ctx context.Context, config ProviderConfig) (ProviderConfig, error) {
	if err := normalizeProviderConfig(&config); err != nil {
		return ProviderConfig{}, err
	}
	query := repository.bind(`INSERT INTO provider_configs(
		id, schema_version, provider_id, provider_type, config_json, config_hash, secret_aliases_json,
		validation_state, validation_json, created_by, created_authentication_type, reason, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if repository.backend == BackendPostgreSQL {
		query = `INSERT INTO provider_configs(
			id, schema_version, provider_id, provider_type, config_json, config_hash, secret_aliases_json,
			validation_state, validation_json, created_by, created_authentication_type, reason, created_at
		) VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7::jsonb, $8, $9::jsonb, $10, $11, $12, $13)`
	}
	_, err := repository.executor.ExecContext(ctx, query,
		config.ID, config.SchemaVersion, config.ProviderID, config.ProviderType, string(config.Config),
		config.ConfigHash, string(config.SecretAliases), config.ValidationState, nullableJSON(config.Validation),
		config.CreatedBy, config.CreatedAuthenticationType, config.Reason, formatTime(config.CreatedAt),
	)
	if err != nil {
		return ProviderConfig{}, mapWriteError(err)
	}
	return config, nil
}

func (repository *providerConfigRepository) Get(ctx context.Context, id string) (ProviderConfig, error) {
	if validateUUID(strings.TrimSpace(id), "provider configuration ID") != nil {
		return ProviderConfig{}, errors.New("provider configuration ID is invalid")
	}
	query := repository.bind(`SELECT id, schema_version, provider_id, provider_type, config_json,
		config_hash, secret_aliases_json, validation_state, validation_json, created_by,
		created_authentication_type, reason, created_at FROM provider_configs WHERE id = ?`)
	config, err := scanProviderConfig(repository.executor.QueryRowContext(ctx, query, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ProviderConfig{}, ErrNotFound
	}
	if err != nil {
		return ProviderConfig{}, databaseError("read provider configuration", err)
	}
	return config, nil
}

func normalizeAdminPolicyConfig(config *AdminPolicyConfig) error {
	if validateUUID(config.ID, "policy configuration ID") != nil {
		return errors.New("policy configuration identity is invalid")
	}
	var err error
	if config.CreatedBy, config.CreatedAuthenticationType, err = normalizeManagementActor(config.CreatedBy, config.CreatedAuthenticationType); err != nil {
		return err
	}
	if config.Spec, err = canonicalJSONObject(config.Spec, "policy configuration spec"); err != nil {
		return err
	}
	config.SpecHash = jsonSHA256(config.Spec)
	config.ValidationState = strings.TrimSpace(config.ValidationState)
	if !validConfigState(config.ValidationState) {
		return errors.New("policy configuration validation state is invalid")
	}
	if config.Validation, err = normalizeJSON(config.Validation, false, "policy configuration validation"); err != nil {
		return err
	}
	if config.Reason, err = normalizeReason(config.Reason); err != nil {
		return err
	}
	return normalizeConfigMetadata(&config.SchemaVersion, &config.CreatedAt)
}

func normalizeProviderConfig(config *ProviderConfig) error {
	if validateUUID(config.ID, "provider configuration ID") != nil {
		return errors.New("provider configuration identity is invalid")
	}
	var err error
	if config.CreatedBy, config.CreatedAuthenticationType, err = normalizeManagementActor(config.CreatedBy, config.CreatedAuthenticationType); err != nil {
		return err
	}
	if config.ProviderID, err = normalizeManagementIdentifier(config.ProviderID, "provider ID"); err != nil {
		return err
	}
	config.ProviderType = strings.TrimSpace(config.ProviderType)
	if config.ProviderType != "oidc" {
		return errors.New("provider type must be oidc")
	}
	if config.Config, err = canonicalJSONObject(config.Config, "provider configuration"); err != nil {
		return err
	}
	config.ConfigHash = jsonSHA256(config.Config)
	if config.SecretAliases, err = normalizeSecretAliases(config.SecretAliases); err != nil {
		return err
	}
	config.ValidationState = strings.TrimSpace(config.ValidationState)
	if !validConfigState(config.ValidationState) {
		return errors.New("provider configuration validation state is invalid")
	}
	if config.Validation, err = normalizeJSON(config.Validation, false, "provider configuration validation"); err != nil {
		return err
	}
	if config.Reason, err = normalizeReason(config.Reason); err != nil {
		return err
	}
	return normalizeConfigMetadata(&config.SchemaVersion, &config.CreatedAt)
}

func normalizeConfigMetadata(schemaVersion *int, createdAt *time.Time) error {
	if *schemaVersion == 0 {
		*schemaVersion = ObjectSchemaVersion
	}
	if *schemaVersion != ObjectSchemaVersion || createdAt.IsZero() {
		return errors.New("management configuration schema or creation time is invalid")
	}
	*createdAt = createdAt.UTC()
	return nil
}

func scanAdminPolicyConfig(row rowScanner) (AdminPolicyConfig, error) {
	var config AdminPolicyConfig
	var spec, validation []byte
	var createdAt string
	if err := row.Scan(&config.ID, &config.SchemaVersion, &spec, &config.SpecHash, &config.ValidationState,
		&validation, &config.CreatedBy, &config.CreatedAuthenticationType, &config.Reason, &createdAt); err != nil {
		return AdminPolicyConfig{}, err
	}
	config.Spec = append(json.RawMessage(nil), spec...)
	var err error
	if config.Spec, err = canonicalJSONObject(config.Spec, "policy configuration spec"); err != nil {
		return AdminPolicyConfig{}, err
	}
	if len(validation) > 0 {
		config.Validation = append(json.RawMessage(nil), validation...)
	}
	if config.CreatedAt, err = parseTime(createdAt, "policy configuration creation time"); err != nil {
		return AdminPolicyConfig{}, err
	}
	return config, nil
}

func scanProviderConfig(row rowScanner) (ProviderConfig, error) {
	var config ProviderConfig
	var document, aliases, validation []byte
	var createdAt string
	if err := row.Scan(&config.ID, &config.SchemaVersion, &config.ProviderID, &config.ProviderType,
		&document, &config.ConfigHash, &aliases, &config.ValidationState, &validation,
		&config.CreatedBy, &config.CreatedAuthenticationType, &config.Reason, &createdAt); err != nil {
		return ProviderConfig{}, err
	}
	config.Config = append(json.RawMessage(nil), document...)
	config.SecretAliases = append(json.RawMessage(nil), aliases...)
	var err error
	if config.Config, err = canonicalJSONObject(config.Config, "provider configuration"); err != nil {
		return ProviderConfig{}, err
	}
	if config.SecretAliases, err = normalizeSecretAliases(config.SecretAliases); err != nil {
		return ProviderConfig{}, err
	}
	if len(validation) > 0 {
		config.Validation = append(json.RawMessage(nil), validation...)
	}
	if config.CreatedAt, err = parseTime(createdAt, "provider configuration creation time"); err != nil {
		return ProviderConfig{}, err
	}
	return config, nil
}
