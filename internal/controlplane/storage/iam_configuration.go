package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type securityPolicyRepository struct{ repositoryBase }

func (repository *securityPolicyRepository) Put(ctx context.Context, policy SecurityPolicy, expectedRevision uint64) (SecurityPolicy, error) {
	if policy.ScopeType != "platform" && policy.ScopeType != "organization" || !json.Valid(policy.Spec) || policy.UpdatedBy == "" {
		return SecurityPolicy{}, errors.New("security policy is invalid")
	}
	if policy.ScopeType == "platform" {
		policy.OrganizationID = ""
	} else if policy.OrganizationID == "" {
		return SecurityPolicy{}, errors.New("organization security policy requires an organization")
	}
	policy.Revision = expectedRevision + 1
	scopeID := policy.OrganizationID
	if policy.ScopeType == "platform" {
		scopeID = "platform"
	}
	if policy.UpdatedAt.IsZero() {
		policy.UpdatedAt = time.Now().UTC()
	}
	if expectedRevision == 0 {
		_, err := repository.executor.ExecContext(ctx, repository.bind(`INSERT INTO security_policies(scope_type,
			scope_id, organization_id, spec_json, revision, updated_by, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`), policy.ScopeType,
			scopeID, nullableString(policy.OrganizationID), string(policy.Spec), policy.Revision, policy.UpdatedBy, formatTime(policy.UpdatedAt))
		if err != nil {
			return SecurityPolicy{}, mapWriteError(err)
		}
		return policy, nil
	}
	result, err := repository.executor.ExecContext(ctx, repository.bind(`UPDATE security_policies SET spec_json = ?,
		revision = ?, updated_by = ?, updated_at = ? WHERE scope_type = ? AND scope_id = ? AND revision = ?`),
		string(policy.Spec), policy.Revision, policy.UpdatedBy, formatTime(policy.UpdatedAt), policy.ScopeType, scopeID, expectedRevision)
	if err != nil {
		return SecurityPolicy{}, mapWriteError(err)
	}
	count, err := rowsAffected(result)
	if err != nil {
		return SecurityPolicy{}, err
	}
	if count != 1 {
		return SecurityPolicy{}, ErrConflict
	}
	return policy, nil
}

func (repository *securityPolicyRepository) GetPlatform(ctx context.Context) (SecurityPolicy, error) {
	return repository.get(ctx, "platform", "")
}

func (repository *securityPolicyRepository) GetOrganization(ctx context.Context, organizationID string) (SecurityPolicy, error) {
	return repository.get(ctx, "organization", organizationID)
}

func (repository *securityPolicyRepository) get(ctx context.Context, scopeType, organizationID string) (SecurityPolicy, error) {
	var policy SecurityPolicy
	var storedOrganization sql.NullString
	var spec, updatedAt string
	scopeID := organizationID
	if scopeType == "platform" {
		scopeID = "platform"
	}
	err := repository.executor.QueryRowContext(ctx, repository.bind(`SELECT scope_type, organization_id, spec_json,
		revision, updated_by, updated_at FROM security_policies WHERE scope_type = ? AND scope_id = ?`),
		scopeType, scopeID).Scan(&policy.ScopeType, &storedOrganization, &spec, &policy.Revision, &policy.UpdatedBy, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return SecurityPolicy{}, ErrNotFound
	}
	if err != nil {
		return SecurityPolicy{}, databaseError("read security policy", err)
	}
	policy.OrganizationID = storedOrganization.String
	policy.Spec = json.RawMessage(spec)
	policy.UpdatedAt, err = parseTime(updatedAt, "security policy update time")
	return policy, err
}
