package storage

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

type organizationRepository struct{ repositoryBase }
type groupRepository struct{ repositoryBase }
type invitationRepository struct{ repositoryBase }

func (repository *organizationRepository) Create(ctx context.Context, organization Organization) error {
	if err := normalizeOrganization(&organization); err != nil {
		return err
	}
	_, err := repository.executor.ExecContext(ctx, repository.bind(`INSERT INTO organizations(
		id, name, slug, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`),
		organization.ID, organization.Name, organization.Slug, organization.Status,
		formatTime(organization.CreatedAt), formatTime(organization.UpdatedAt))
	return mapWriteError(err)
}

func (repository *organizationRepository) Get(ctx context.Context, id string) (Organization, error) {
	if err := validateUUID(id, "organization ID"); err != nil {
		return Organization{}, err
	}
	return repository.get(ctx, `WHERE id = ?`, id)
}

func (repository *organizationRepository) get(ctx context.Context, where string, argument any) (Organization, error) {
	var organization Organization
	var createdAt, updatedAt string
	err := repository.executor.QueryRowContext(ctx, repository.bind(`SELECT id, name, slug, status, created_at, updated_at
		FROM organizations `+where), argument).Scan(&organization.ID, &organization.Name, &organization.Slug,
		&organization.Status, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Organization{}, ErrNotFound
	}
	if err != nil {
		return Organization{}, databaseError("read organization", err)
	}
	organization.CreatedAt, err = parseTime(createdAt, "organization creation time")
	if err == nil {
		organization.UpdatedAt, err = parseTime(updatedAt, "organization update time")
	}
	return organization, err
}

func (repository *organizationRepository) List(ctx context.Context, limit int) ([]Organization, error) {
	limit, _, err := normalizePage(limit, nil)
	if err != nil {
		return nil, err
	}
	rows, err := repository.executor.QueryContext(ctx, repository.bind(`SELECT id FROM organizations ORDER BY name, id LIMIT ?`), limit)
	if err != nil {
		return nil, databaseError("list organizations", err)
	}
	ids, err := collectStringColumn(rows, "organizations")
	if err != nil {
		return nil, err
	}
	organizations := make([]Organization, 0, len(ids))
	for _, id := range ids {
		organization, err := repository.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		organizations = append(organizations, organization)
	}
	return organizations, nil
}

func (repository *organizationRepository) AddMember(ctx context.Context, membership OrganizationMembership) error {
	if err := validateUUID(membership.OrganizationID, "organization ID"); err != nil {
		return err
	}
	if err := validateUUID(membership.IdentityID, "identity ID"); err != nil {
		return err
	}
	if membership.Status == "" {
		membership.Status = "active"
	}
	if membership.Status != "active" && membership.Status != "suspended" {
		return errors.New("organization membership status is invalid")
	}
	now := time.Now().UTC()
	if membership.CreatedAt.IsZero() {
		membership.CreatedAt = now
	}
	if membership.UpdatedAt.IsZero() {
		membership.UpdatedAt = membership.CreatedAt
	}
	_, err := repository.executor.ExecContext(ctx, repository.bind(`INSERT INTO organization_memberships(
		organization_id, identity_id, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`),
		membership.OrganizationID, membership.IdentityID, membership.Status,
		formatTime(membership.CreatedAt), formatTime(membership.UpdatedAt))
	return mapWriteError(err)
}

func (repository *organizationRepository) RemoveMember(ctx context.Context, organizationID, identityID string) error {
	result, err := repository.executor.ExecContext(ctx, repository.bind(`DELETE FROM organization_memberships
		WHERE organization_id = ? AND identity_id = ?`), organizationID, identityID)
	if err != nil {
		return mapWriteError(err)
	}
	count, err := rowsAffected(result)
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrNotFound
	}
	return nil
}

func (repository *organizationRepository) ListMembers(ctx context.Context, organizationID string, limit int) ([]OrganizationMembership, error) {
	limit, _, err := normalizePage(limit, nil)
	if err != nil {
		return nil, err
	}
	rows, err := repository.executor.QueryContext(ctx, repository.bind(`SELECT organization_id, identity_id, status,
		created_at, updated_at FROM organization_memberships WHERE organization_id = ? ORDER BY created_at, identity_id LIMIT ?`),
		organizationID, limit)
	if err != nil {
		return nil, databaseError("list organization members", err)
	}
	defer rows.Close()
	members := make([]OrganizationMembership, 0)
	for rows.Next() {
		var member OrganizationMembership
		var createdAt, updatedAt string
		if err := rows.Scan(&member.OrganizationID, &member.IdentityID, &member.Status, &createdAt, &updatedAt); err != nil {
			return nil, databaseError("decode organization member", err)
		}
		member.CreatedAt, err = parseTime(createdAt, "organization membership creation time")
		if err == nil {
			member.UpdatedAt, err = parseTime(updatedAt, "organization membership update time")
		}
		if err != nil {
			return nil, err
		}
		members = append(members, member)
	}
	return members, databaseError("iterate organization members", rows.Err())
}

func (repository *organizationRepository) ListForIdentity(ctx context.Context, identityID string) ([]Organization, error) {
	rows, err := repository.executor.QueryContext(ctx, repository.bind(`SELECT organization_id FROM organization_memberships
		WHERE identity_id = ? AND status = 'active' ORDER BY organization_id`), identityID)
	if err != nil {
		return nil, databaseError("list identity organizations", err)
	}
	ids, err := collectStringColumn(rows, "identity organizations")
	if err != nil {
		return nil, err
	}
	organizations := make([]Organization, 0, len(ids))
	for _, id := range ids {
		organization, err := repository.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		organizations = append(organizations, organization)
	}
	return organizations, nil
}

func normalizeOrganization(organization *Organization) error {
	if _, err := uuid.Parse(organization.ID); err != nil {
		return errors.New("organization ID must be a UUID")
	}
	organization.Name = strings.TrimSpace(organization.Name)
	organization.Slug = strings.ToLower(strings.TrimSpace(organization.Slug))
	organization.Status = strings.TrimSpace(organization.Status)
	if organization.Name == "" || len(organization.Name) > 256 || organization.Slug == "" || len(organization.Slug) > 63 {
		return errors.New("organization identity is invalid")
	}
	for _, character := range organization.Slug {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
			return errors.New("organization slug is invalid")
		}
	}
	if organization.Status != "active" && organization.Status != "suspended" {
		return errors.New("organization status is invalid")
	}
	now := time.Now().UTC()
	if organization.CreatedAt.IsZero() {
		organization.CreatedAt = now
	}
	if organization.UpdatedAt.IsZero() {
		organization.UpdatedAt = organization.CreatedAt
	}
	return nil
}

func (repository *groupRepository) Create(ctx context.Context, group Group) error {
	if err := normalizeGroup(&group); err != nil {
		return err
	}
	_, err := repository.executor.ExecContext(ctx, repository.bind(`INSERT INTO iam_groups(
		id, organization_id, name, description, system, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`),
		group.ID, group.OrganizationID, group.Name, group.Description, group.System, formatTime(group.CreatedAt), formatTime(group.UpdatedAt))
	return mapWriteError(err)
}

func (repository *groupRepository) Get(ctx context.Context, id string) (Group, error) {
	var group Group
	var createdAt, updatedAt string
	err := repository.executor.QueryRowContext(ctx, repository.bind(`SELECT id, organization_id, name, description, system,
		created_at, updated_at FROM iam_groups WHERE id = ?`), id).Scan(&group.ID, &group.OrganizationID, &group.Name,
		&group.Description, &group.System, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Group{}, ErrNotFound
	}
	if err != nil {
		return Group{}, databaseError("read group", err)
	}
	group.CreatedAt, err = parseTime(createdAt, "group creation time")
	if err == nil {
		group.UpdatedAt, err = parseTime(updatedAt, "group update time")
	}
	return group, err
}

func (repository *groupRepository) List(ctx context.Context, organizationID string, limit int) ([]Group, error) {
	limit, _, err := normalizePage(limit, nil)
	if err != nil {
		return nil, err
	}
	rows, err := repository.executor.QueryContext(ctx, repository.bind(`SELECT id FROM iam_groups
		WHERE organization_id = ? ORDER BY name, id LIMIT ?`), organizationID, limit)
	if err != nil {
		return nil, databaseError("list groups", err)
	}
	ids, err := collectStringColumn(rows, "groups")
	if err != nil {
		return nil, err
	}
	groups := make([]Group, 0, len(ids))
	for _, id := range ids {
		group, err := repository.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	return groups, nil
}

func (repository *groupRepository) Update(ctx context.Context, group Group) error {
	if err := normalizeGroup(&group); err != nil {
		return err
	}
	result, err := repository.executor.ExecContext(ctx, repository.bind(`UPDATE iam_groups SET name = ?, description = ?,
		updated_at = ? WHERE id = ? AND organization_id = ?`), group.Name, group.Description,
		formatTime(group.UpdatedAt), group.ID, group.OrganizationID)
	if err != nil {
		return mapWriteError(err)
	}
	count, err := rowsAffected(result)
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrNotFound
	}
	return nil
}

func (repository *groupRepository) Delete(ctx context.Context, id string) error {
	result, err := repository.executor.ExecContext(ctx, repository.bind(`DELETE FROM iam_groups WHERE id = ? AND system = ?`), id, false)
	if err != nil {
		return mapWriteError(err)
	}
	count, err := rowsAffected(result)
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrNotFound
	}
	return nil
}

func (repository *groupRepository) AddMember(ctx context.Context, member GroupMembership) error {
	if err := validateUUID(member.GroupID, "group ID"); err != nil {
		return err
	}
	if err := validateUUID(member.IdentityID, "identity ID"); err != nil {
		return err
	}
	if member.CreatedAt.IsZero() {
		member.CreatedAt = time.Now().UTC()
	}
	member.SourceType, member.SourceID = strings.TrimSpace(member.SourceType), strings.TrimSpace(member.SourceID)
	if member.SourceType == "" {
		member.SourceType = "manual"
	}
	if member.SourceType != "manual" || member.SourceID != "" {
		return errors.New("group membership source is invalid")
	}
	_, err := repository.executor.ExecContext(ctx, repository.bind(`INSERT INTO group_memberships(
		group_id, identity_id, source_type, source_id, created_at) VALUES (?, ?, ?, ?, ?)`), member.GroupID,
		member.IdentityID, member.SourceType, member.SourceID, formatTime(member.CreatedAt))
	return mapWriteError(err)
}

func (repository *groupRepository) RemoveMember(ctx context.Context, groupID, identityID string) error {
	result, err := repository.executor.ExecContext(ctx, repository.bind(`DELETE FROM group_memberships
		WHERE group_id = ? AND identity_id = ? AND source_type = 'manual'`), groupID, identityID)
	if err != nil {
		return mapWriteError(err)
	}
	count, err := rowsAffected(result)
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrNotFound
	}
	return nil
}

func (repository *groupRepository) ListMembers(ctx context.Context, groupID string, limit int) ([]GroupMembership, error) {
	limit, _, err := normalizePage(limit, nil)
	if err != nil {
		return nil, err
	}
	rows, err := repository.executor.QueryContext(ctx, repository.bind(`SELECT group_id, identity_id, source_type, source_id, created_at
		FROM group_memberships WHERE group_id = ? ORDER BY created_at, identity_id LIMIT ?`), groupID, limit)
	if err != nil {
		return nil, databaseError("list group members", err)
	}
	defer rows.Close()
	members := make([]GroupMembership, 0)
	for rows.Next() {
		var member GroupMembership
		var createdAt string
		if err := rows.Scan(&member.GroupID, &member.IdentityID, &member.SourceType, &member.SourceID, &createdAt); err != nil {
			return nil, databaseError("decode group member", err)
		}
		member.CreatedAt, err = parseTime(createdAt, "group membership creation time")
		if err != nil {
			return nil, err
		}
		members = append(members, member)
	}
	return members, databaseError("iterate group members", rows.Err())
}

func (repository *groupRepository) ListForIdentity(ctx context.Context, organizationID, identityID string) ([]Group, error) {
	rows, err := repository.executor.QueryContext(ctx, repository.bind(`SELECT DISTINCT g.id FROM iam_groups g
		JOIN group_memberships m ON m.group_id = g.id WHERE g.organization_id = ? AND m.identity_id = ? ORDER BY g.name`),
		organizationID, identityID)
	if err != nil {
		return nil, databaseError("list identity groups", err)
	}
	ids, err := collectStringColumn(rows, "identity groups")
	if err != nil {
		return nil, err
	}
	groups := make([]Group, 0, len(ids))
	for _, id := range ids {
		group, err := repository.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	return groups, nil
}

func (repository *groupRepository) PutNamespace(ctx context.Context, item GroupNamespace) error {
	if err := validateUUID(item.GroupID, "group ID"); err != nil {
		return err
	}
	item.Namespace = strings.TrimSpace(item.Namespace)
	if item.Namespace == "" || len(item.Namespace) > 253 {
		return errors.New("namespace is invalid")
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	_, err := repository.executor.ExecContext(ctx, repository.bind(`INSERT INTO group_namespaces(group_id, namespace, created_at)
		VALUES (?, ?, ?)`), item.GroupID, item.Namespace, formatTime(item.CreatedAt))
	return mapWriteError(err)
}

func (repository *groupRepository) DeleteNamespace(ctx context.Context, groupID, namespace string) error {
	result, err := repository.executor.ExecContext(ctx, repository.bind(`DELETE FROM group_namespaces
		WHERE group_id = ? AND namespace = ?`), strings.TrimSpace(groupID), strings.TrimSpace(namespace))
	if err != nil {
		return mapWriteError(err)
	}
	return expectOne(result)
}

func (repository *groupRepository) ListNamespaces(ctx context.Context, groupID string) ([]GroupNamespace, error) {
	rows, err := repository.executor.QueryContext(ctx, repository.bind(`SELECT group_id, namespace, created_at
		FROM group_namespaces WHERE group_id = ? ORDER BY namespace`), strings.TrimSpace(groupID))
	if err != nil {
		return nil, databaseError("list group namespaces", err)
	}
	defer rows.Close()
	items := make([]GroupNamespace, 0)
	for rows.Next() {
		var item GroupNamespace
		var createdAt string
		if err := rows.Scan(&item.GroupID, &item.Namespace, &createdAt); err != nil {
			return nil, databaseError("decode group namespace", err)
		}
		item.CreatedAt, err = parseTime(createdAt, "group namespace creation time")
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, databaseError("iterate group namespaces", rows.Err())
}

func (repository *groupRepository) ListAuthorizedNamespaces(ctx context.Context, identityID string, groupIDs []string) ([]string, error) {
	administrator, err := repository.IsAdministrator(ctx, identityID, groupIDs)
	if err != nil {
		return nil, err
	}
	if administrator {
		return []string{"*"}, nil
	}
	ids := append([]string(nil), groupIDs...)
	if len(ids) == 0 {
		groups, listErr := repository.listAllForIdentity(ctx, identityID)
		if listErr != nil {
			return nil, listErr
		}
		for _, group := range groups {
			ids = append(ids, group.ID)
		}
	}
	result := make([]string, 0)
	seen := make(map[string]struct{})
	for _, groupID := range ids {
		items, listErr := repository.ListNamespaces(ctx, groupID)
		if listErr != nil {
			return nil, listErr
		}
		for _, item := range items {
			if _, exists := seen[item.Namespace]; !exists {
				seen[item.Namespace] = struct{}{}
				result = append(result, item.Namespace)
			}
		}
	}
	slices.Sort(result)
	return result, nil
}

func (repository *groupRepository) IsAdministrator(ctx context.Context, identityID string, groupIDs []string) (bool, error) {
	if len(groupIDs) > 0 {
		for _, groupID := range groupIDs {
			group, err := repository.Get(ctx, groupID)
			if err != nil {
				return false, err
			}
			if group.System {
				return true, nil
			}
		}
		return false, nil
	}
	groups, err := repository.listAllForIdentity(ctx, identityID)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(groups, func(group Group) bool { return group.System }), nil
}

func (repository *groupRepository) listAllForIdentity(ctx context.Context, identityID string) ([]Group, error) {
	rows, err := repository.executor.QueryContext(ctx, repository.bind(`SELECT DISTINCT g.id FROM iam_groups g
		JOIN group_memberships m ON m.group_id = g.id WHERE m.identity_id = ? ORDER BY g.name`), identityID)
	if err != nil {
		return nil, databaseError("list identity groups", err)
	}
	ids, err := collectStringColumn(rows, "identity groups")
	if err != nil {
		return nil, err
	}
	groups := make([]Group, 0, len(ids))
	for _, id := range ids {
		group, getErr := repository.Get(ctx, id)
		if getErr != nil {
			return nil, getErr
		}
		groups = append(groups, group)
	}
	return groups, nil
}

func normalizeGroup(group *Group) error {
	if err := validateUUID(group.ID, "group ID"); err != nil {
		return err
	}
	if err := validateUUID(group.OrganizationID, "organization ID"); err != nil {
		return err
	}
	group.Name = strings.TrimSpace(group.Name)
	group.Description = strings.TrimSpace(group.Description)
	if group.Name == "" || len(group.Name) > 128 || len(group.Description) > 1024 {
		return errors.New("group is invalid")
	}
	now := time.Now().UTC()
	if group.CreatedAt.IsZero() {
		group.CreatedAt = now
	}
	if group.UpdatedAt.IsZero() {
		group.UpdatedAt = group.CreatedAt
	}
	return nil
}

func (repository *invitationRepository) Create(ctx context.Context, invitation Invitation) error {
	if err := normalizeInvitation(&invitation); err != nil {
		return err
	}
	_, err := repository.executor.ExecContext(ctx, repository.bind(`INSERT INTO invitations(id, organization_id, email,
		group_id, token_hash, status, invited_by, created_at, expires_at, accepted_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		invitation.ID, invitation.OrganizationID, invitation.Email, invitation.GroupID, invitation.TokenHash,
		invitation.Status, invitation.InvitedBy, formatTime(invitation.CreatedAt), formatTime(invitation.ExpiresAt),
		nullableTime(invitation.AcceptedAt))
	return mapWriteError(err)
}

func (repository *invitationRepository) GetByTokenHash(ctx context.Context, hash []byte, now time.Time) (Invitation, error) {
	return repository.get(ctx, `WHERE token_hash = ? AND status = 'pending' AND expires_at > ?`, hash, formatTime(now))
}

func (repository *invitationRepository) List(ctx context.Context, organizationID string, limit int) ([]Invitation, error) {
	limit, _, err := normalizePage(limit, nil)
	if err != nil {
		return nil, err
	}
	rows, err := repository.executor.QueryContext(ctx, repository.bind(`SELECT id FROM invitations
		WHERE organization_id = ? ORDER BY created_at DESC, id DESC LIMIT ?`), organizationID, limit)
	if err != nil {
		return nil, databaseError("list invitations", err)
	}
	ids, err := collectStringColumn(rows, "invitations")
	if err != nil {
		return nil, err
	}
	invitations := make([]Invitation, 0, len(ids))
	for _, id := range ids {
		invitation, err := repository.get(ctx, `WHERE id = ?`, id)
		if err != nil {
			return nil, err
		}
		invitations = append(invitations, invitation)
	}
	return invitations, nil
}

func (repository *invitationRepository) get(ctx context.Context, where string, arguments ...any) (Invitation, error) {
	var invitation Invitation
	var createdAt, expiresAt string
	var acceptedAt sql.NullString
	err := repository.executor.QueryRowContext(ctx, repository.bind(`SELECT id, organization_id, email, group_id, token_hash,
		status, invited_by, created_at, expires_at, accepted_at FROM invitations `+where), arguments...).Scan(
		&invitation.ID, &invitation.OrganizationID, &invitation.Email, &invitation.GroupID, &invitation.TokenHash,
		&invitation.Status, &invitation.InvitedBy, &createdAt, &expiresAt, &acceptedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Invitation{}, ErrNotFound
	}
	if err != nil {
		return Invitation{}, databaseError("read invitation", err)
	}
	invitation.CreatedAt, err = parseTime(createdAt, "invitation creation time")
	if err == nil {
		invitation.ExpiresAt, err = parseTime(expiresAt, "invitation expiry")
	}
	if err == nil {
		invitation.AcceptedAt, err = parseNullableTime(acceptedAt, "invitation acceptance time")
	}
	return invitation, err
}

func (repository *invitationRepository) Accept(ctx context.Context, id, identityID string, at time.Time) error {
	return repository.updateStatus(ctx, id, "accepted", identityID, at)
}

func (repository *invitationRepository) Revoke(ctx context.Context, id string, at time.Time) error {
	return repository.updateStatus(ctx, id, "revoked", "", at)
}

func (repository *invitationRepository) updateStatus(ctx context.Context, id, status, identityID string, at time.Time) error {
	if at.IsZero() {
		return errors.New("invitation status time is required")
	}
	query := `UPDATE invitations SET status = ?, accepted_at = ? WHERE id = ? AND status = 'pending'`
	acceptedAt := any(nil)
	if status == "accepted" {
		if err := validateUUID(identityID, "identity ID"); err != nil {
			return err
		}
		acceptedAt = formatTime(at)
	}
	result, err := repository.executor.ExecContext(ctx, repository.bind(query), status, acceptedAt, id)
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

func normalizeInvitation(invitation *Invitation) error {
	if err := validateUUID(invitation.ID, "invitation ID"); err != nil {
		return err
	}
	if err := validateUUID(invitation.OrganizationID, "organization ID"); err != nil {
		return err
	}
	if err := validateUUID(invitation.InvitedBy, "inviting identity ID"); err != nil {
		return err
	}
	invitation.Email = strings.ToLower(strings.TrimSpace(invitation.Email))
	invitation.GroupID = strings.TrimSpace(invitation.GroupID)
	if invitation.Email == "" || !strings.Contains(invitation.Email, "@") || uuid.Validate(invitation.GroupID) != nil || len(invitation.TokenHash) != 32 {
		return errors.New("invitation is invalid")
	}
	if invitation.Status == "" {
		invitation.Status = "pending"
	}
	if invitation.Status != "pending" {
		return errors.New("new invitation status is invalid")
	}
	if invitation.CreatedAt.IsZero() {
		invitation.CreatedAt = time.Now().UTC()
	}
	if !invitation.ExpiresAt.After(invitation.CreatedAt) {
		return errors.New("invitation expiry is invalid")
	}
	return nil
}
