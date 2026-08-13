package storage

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

var managementRolePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{2,63}$`)

type adminAssignmentRepository struct{ repositoryBase }

func (repository *adminAssignmentRepository) Create(ctx context.Context, assignment AdminAssignment) error {
	if err := normalizeAdminAssignment(&assignment); err != nil {
		return err
	}
	query := repository.bind(`INSERT INTO admin_assignments(
		id, schema_version, policy_revision, role, subjects_json, groups_json, namespaces_json, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if repository.backend == BackendPostgreSQL {
		query = `INSERT INTO admin_assignments(
			id, schema_version, policy_revision, role, subjects_json, groups_json, namespaces_json, created_at
		) VALUES ($1, $2, $3, $4, $5::jsonb, $6::jsonb, $7::jsonb, $8)`
	}
	_, err := repository.executor.ExecContext(ctx, query,
		assignment.ID, assignment.SchemaVersion, assignment.PolicyRevision, assignment.Role,
		string(assignment.Subjects), string(assignment.Groups), string(assignment.Namespaces),
		formatTime(assignment.CreatedAt),
	)
	return mapWriteError(err)
}

func (repository *adminAssignmentRepository) ListByPolicyRevision(ctx context.Context, revision uint64) ([]AdminAssignment, error) {
	if revision == 0 {
		return nil, errors.New("policy revision must be positive")
	}
	query := repository.bind(`SELECT id, schema_version, policy_revision, role, subjects_json, groups_json,
		namespaces_json, created_at FROM admin_assignments WHERE policy_revision = ? ORDER BY role, id`)
	rows, err := repository.executor.QueryContext(ctx, query, revision)
	if err != nil {
		return nil, databaseError("list management assignments", err)
	}
	defer rows.Close()
	assignments := make([]AdminAssignment, 0)
	for rows.Next() {
		assignment, err := scanAdminAssignment(rows)
		if err != nil {
			return nil, err
		}
		assignments = append(assignments, assignment)
	}
	if err := rows.Err(); err != nil {
		return nil, databaseError("iterate management assignments", err)
	}
	return assignments, nil
}

func normalizeAdminAssignment(assignment *AdminAssignment) error {
	if validateUUID(assignment.ID, "management assignment ID") != nil || assignment.PolicyRevision == 0 {
		return errors.New("management assignment identity is invalid")
	}
	assignment.Role = strings.TrimSpace(assignment.Role)
	if !managementRolePattern.MatchString(assignment.Role) {
		return errors.New("management assignment role is invalid")
	}
	var err error
	var subjects, groups []string
	if assignment.Subjects, subjects, err = normalizeAssignmentValues(assignment.Subjects, "subjects", true); err != nil {
		return err
	}
	if assignment.Groups, groups, err = normalizeAssignmentValues(assignment.Groups, "groups", false); err != nil {
		return err
	}
	if len(subjects) == 0 && len(groups) == 0 {
		return errors.New("management assignment requires subjects or groups")
	}
	namespacesJSON, namespaces, err := normalizeNamespaces(assignment.Namespaces)
	if err != nil {
		return err
	}
	if assignment.Role == "namespace-admin" && len(namespaces) == 0 {
		return errors.New("namespace-admin assignment requires namespace scope")
	}
	if assignment.Role != "namespace-admin" && isBuiltInManagementRole(assignment.Role) && len(namespaces) != 0 {
		return errors.New("only namespace-admin assignments may contain namespace scope")
	}
	assignment.Namespaces = namespacesJSON
	if assignment.SchemaVersion == 0 {
		assignment.SchemaVersion = ObjectSchemaVersion
	}
	if assignment.SchemaVersion != ObjectSchemaVersion || assignment.CreatedAt.IsZero() {
		return errors.New("management assignment schema or creation time is invalid")
	}
	assignment.CreatedAt = assignment.CreatedAt.UTC()
	return nil
}

func isBuiltInManagementRole(role string) bool {
	switch role {
	case "platform-admin", "security-admin", "operator", "auditor", "namespace-admin":
		return true
	default:
		return false
	}
}

func scanAdminAssignment(row rowScanner) (AdminAssignment, error) {
	var assignment AdminAssignment
	var subjects, groups, namespaces []byte
	var createdAt string
	if err := row.Scan(&assignment.ID, &assignment.SchemaVersion, &assignment.PolicyRevision, &assignment.Role,
		&subjects, &groups, &namespaces, &createdAt); err != nil {
		return AdminAssignment{}, err
	}
	assignment.Subjects = append(json.RawMessage(nil), subjects...)
	assignment.Groups = append(json.RawMessage(nil), groups...)
	assignment.Namespaces = append(json.RawMessage(nil), namespaces...)
	var err error
	if assignment.Subjects, _, err = normalizeAssignmentValues(assignment.Subjects, "subjects", true); err != nil {
		return AdminAssignment{}, err
	}
	if assignment.Groups, _, err = normalizeAssignmentValues(assignment.Groups, "groups", false); err != nil {
		return AdminAssignment{}, err
	}
	if assignment.Namespaces, _, err = normalizeNamespaces(assignment.Namespaces); err != nil {
		return AdminAssignment{}, err
	}
	if assignment.CreatedAt, err = parseTime(createdAt, "management assignment creation time"); err != nil {
		return AdminAssignment{}, err
	}
	return assignment, nil
}
