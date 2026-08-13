package storage

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

type authorizationDefinitionRepository struct{ repositoryBase }

func (repository *authorizationDefinitionRepository) CreateRole(ctx context.Context, role AuthorizationRoleRecord) error {
	role.ID = strings.TrimSpace(role.ID)
	if role.Revision == 0 || role.ID == "" || !json.Valid(role.Definition) {
		return errors.New("authorization role record is invalid")
	}
	query := repository.bind(`INSERT INTO authorization_roles(revision, id, definition_json) VALUES (?, ?, ?)`)
	if repository.backend == BackendPostgreSQL {
		query = `INSERT INTO authorization_roles(revision, id, definition_json) VALUES ($1, $2, $3::jsonb)`
	}
	_, err := repository.executor.ExecContext(ctx, query, role.Revision, role.ID, string(role.Definition))
	return mapWriteError(err)
}

func (repository *authorizationDefinitionRepository) CreateBinding(ctx context.Context, binding AuthorizationBindingRecord) error {
	binding.ID, binding.RoleID = strings.TrimSpace(binding.ID), strings.TrimSpace(binding.RoleID)
	if binding.Revision == 0 || binding.ID == "" || binding.RoleID == "" ||
		!json.Valid(binding.NamespaceNames) || !json.Valid(binding.LabelSelectors) || !json.Valid(binding.Binding) {
		return errors.New("authorization binding record is invalid")
	}
	query := repository.bind(`INSERT INTO authorization_bindings(
		revision, id, role_id, subject_type, principal_id, provider_id, group_name, scope_type,
		namespace_names_json, label_selectors_json, managed_by, created_by, binding_json
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if repository.backend == BackendPostgreSQL {
		query = `INSERT INTO authorization_bindings(
			revision, id, role_id, subject_type, principal_id, provider_id, group_name, scope_type,
			namespace_names_json, label_selectors_json, managed_by, created_by, binding_json
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb, $10::jsonb, $11, $12, $13::jsonb)`
	}
	_, err := repository.executor.ExecContext(ctx, query,
		binding.Revision, binding.ID, binding.RoleID, binding.SubjectType,
		nullableString(binding.PrincipalID), nullableString(binding.ProviderID), nullableString(binding.GroupName),
		binding.ScopeType, string(binding.NamespaceNames), string(binding.LabelSelectors), binding.ManagedBy,
		binding.CreatedBy, string(binding.Binding),
	)
	return mapWriteError(err)
}
