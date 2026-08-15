package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
)

func (api *readAPI) authorizeIAM(ctx *echo.Context, capability adminauthorization.Capability, organizationID, namespace string) bool {
	decision := api.authorizer.Authorize(ctx.Request().Context(), subjectFromRequest(ctx.Request()), adminauthorization.Request{
		Capability: capability, OrganizationID: organizationID, Namespace: namespace,
	})
	if decision.Allowed {
		return true
	}
	writeError(ctx.Response(), http.StatusForbidden, "forbidden", "operation is not permitted", requestID(ctx.Request()))
	return false
}

func (api *readAPI) authorizePlatformOrOrganization(ctx *echo.Context, platform, organization adminauthorization.Capability, organizationID string) bool {
	if api.authorizer.Authorize(ctx.Request().Context(), subjectFromRequest(ctx.Request()), adminauthorization.Request{Capability: platform}).Allowed {
		return true
	}
	return api.authorizeIAM(ctx, organization, organizationID, "")
}

func (api *readAPI) listOrganizations(ctx *echo.Context) error {
	subject := subjectFromRequest(ctx.Request())
	var items []storage.Organization
	var err error
	if api.authorizer.Authorize(ctx.Request().Context(), subject, adminauthorization.Request{Capability: "platform.overview.read"}).Allowed {
		items, err = api.status.Organizations().List(ctx.Request().Context(), 100)
	} else {
		items, err = api.status.Organizations().ListForIdentity(ctx.Request().Context(), subject.ID)
	}
	if err != nil {
		return api.iamStorageError(ctx, err)
	}
	return ctx.JSON(http.StatusOK, map[string]any{"items": items})
}

func (api *readAPI) getOrganization(ctx *echo.Context) error {
	id := ctx.Param("organizationID")
	if !api.authorizePlatformOrOrganization(ctx, "platform.overview.read", "org.organization.read", id) {
		return nil
	}
	item, err := api.status.Organizations().Get(ctx.Request().Context(), id)
	if err != nil {
		return api.iamStorageError(ctx, err)
	}
	ctx.Response().Header().Set("ETag", iamETag(item.UpdatedAt))
	return ctx.JSON(http.StatusOK, item)
}

func (api *readAPI) authorizeIdentityManagement(ctx *echo.Context, identityID string, write bool) bool {
	platformCapability := adminauthorization.Capability("platform.identity.users.read")
	organizationCapability := adminauthorization.Capability("org.identities.read")
	if write {
		platformCapability = "platform.identity.users.manage"
		organizationCapability = "org.identities.manage"
	}
	subject := subjectFromRequest(ctx.Request())
	if api.authorizer.Authorize(ctx.Request().Context(), subject, adminauthorization.Request{Capability: platformCapability}).Allowed {
		return true
	}
	organizations, err := api.status.Organizations().ListForIdentity(ctx.Request().Context(), identityID)
	if err == nil && slices.ContainsFunc(organizations, func(organization storage.Organization) bool {
		return api.authorizer.Authorize(ctx.Request().Context(), subject, adminauthorization.Request{
			Capability: organizationCapability, OrganizationID: organization.ID,
		}).Allowed
	}) {
		return true
	}
	writeError(ctx.Response(), http.StatusForbidden, "forbidden", "operation is not permitted", requestID(ctx.Request()))
	return false
}

type groupInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Reason      string `json:"reason"`
}

func (api *readAPI) listGroups(ctx *echo.Context) error {
	organizationID := ctx.Param("organizationID")
	if !api.authorizePlatformOrOrganization(ctx, "platform.identity.identities.read", "org.groups.read", organizationID) {
		return nil
	}
	items, err := api.status.Groups().List(ctx.Request().Context(), organizationID, 100)
	if err != nil {
		return api.iamStorageError(ctx, err)
	}
	return ctx.JSON(http.StatusOK, map[string]any{"items": items})
}

func (api *readAPI) createGroup(ctx *echo.Context) error {
	organizationID := ctx.Param("organizationID")
	if !api.authorizePlatformOrOrganization(ctx, "platform.identity.users.manage", "org.groups.manage", organizationID) {
		return nil
	}
	var input groupInput
	if !decodePolicyJSON(ctx.Response(), ctx.Request(), &input) || !validChangeReason(input.Reason) {
		return api.invalidIAMMutation(ctx)
	}
	now := time.Now().UTC()
	item := storage.Group{ID: uuid.NewString(), OrganizationID: organizationID, Name: input.Name,
		Description: input.Description, CreatedAt: now, UpdatedAt: now}
	if err := api.status.Groups().Create(ctx.Request().Context(), item); err != nil {
		return api.iamStorageError(ctx, err)
	}
	api.audit(ctx.Request(), subjectFromRequest(ctx.Request()), "iam.group.create", "success")
	return ctx.JSON(http.StatusCreated, item)
}

func (api *readAPI) updateGroup(ctx *echo.Context) error {
	organizationID, groupID := ctx.Param("organizationID"), ctx.Param("groupID")
	if !api.authorizePlatformOrOrganization(ctx, "platform.identity.users.manage", "org.groups.manage", organizationID) {
		return nil
	}
	existing, err := api.status.Groups().Get(ctx.Request().Context(), groupID)
	if err != nil || existing.OrganizationID != organizationID {
		return api.iamStorageError(ctx, storage.ErrNotFound)
	}
	if !requireIAMETag(ctx, existing.UpdatedAt) {
		return nil
	}
	var input groupInput
	if !decodePolicyJSON(ctx.Response(), ctx.Request(), &input) || !validChangeReason(input.Reason) {
		return api.invalidIAMMutation(ctx)
	}
	existing.Name, existing.Description, existing.UpdatedAt = input.Name, input.Description, time.Now().UTC()
	if err := api.status.Groups().Update(ctx.Request().Context(), existing); err != nil {
		return api.iamStorageError(ctx, err)
	}
	ctx.Response().Header().Set("ETag", iamETag(existing.UpdatedAt))
	api.audit(ctx.Request(), subjectFromRequest(ctx.Request()), "iam.group.update", "success")
	return ctx.JSON(http.StatusOK, existing)
}

func (api *readAPI) deleteGroup(ctx *echo.Context) error {
	organizationID, groupID := ctx.Param("organizationID"), ctx.Param("groupID")
	if !api.authorizePlatformOrOrganization(ctx, "platform.identity.users.manage", "org.groups.manage", organizationID) {
		return nil
	}
	group, err := api.status.Groups().Get(ctx.Request().Context(), groupID)
	if err != nil || group.OrganizationID != organizationID {
		return api.iamStorageError(ctx, storage.ErrNotFound)
	}
	if !validChangeReason(ctx.Request().Header.Get("X-KubeLoop-Reason")) {
		return api.invalidIAMMutation(ctx)
	}
	if err := api.status.Groups().Delete(ctx.Request().Context(), groupID); err != nil {
		return api.iamStorageError(ctx, err)
	}
	api.audit(ctx.Request(), subjectFromRequest(ctx.Request()), "iam.group.delete", "success")
	return ctx.NoContent(http.StatusNoContent)
}

func (api *readAPI) listGroupMemberships(ctx *echo.Context) error {
	organizationID, groupID := ctx.Param("organizationID"), ctx.Param("groupID")
	if !api.authorizePlatformOrOrganization(ctx, "platform.identity.identities.read", "org.groups.read", organizationID) {
		return nil
	}
	group, err := api.status.Groups().Get(ctx.Request().Context(), groupID)
	if err != nil || group.OrganizationID != organizationID {
		return api.iamStorageError(ctx, storage.ErrNotFound)
	}
	items, err := api.status.Groups().ListMembers(ctx.Request().Context(), groupID, 100)
	if err != nil {
		return api.iamStorageError(ctx, err)
	}
	return ctx.JSON(http.StatusOK, map[string]any{"items": items})
}

func (api *readAPI) addGroupMembership(ctx *echo.Context) error {
	organizationID, groupID := ctx.Param("organizationID"), ctx.Param("groupID")
	if !api.authorizePlatformOrOrganization(ctx, "platform.identity.users.manage", "org.groups.manage", organizationID) {
		return nil
	}
	var input struct {
		IdentityID string `json:"identityId"`
		Reason     string `json:"reason"`
	}
	if !decodePolicyJSON(ctx.Response(), ctx.Request(), &input) || !validChangeReason(input.Reason) {
		return api.invalidIAMMutation(ctx)
	}
	group, err := api.status.Groups().Get(ctx.Request().Context(), groupID)
	if err != nil || group.OrganizationID != organizationID || !api.identityIsOrganizationMember(ctx, organizationID, input.IdentityID) {
		return api.iamStorageError(ctx, storage.ErrNotFound)
	}
	item := storage.GroupMembership{GroupID: groupID, IdentityID: input.IdentityID, CreatedAt: time.Now().UTC()}
	if err := api.status.Groups().AddMember(ctx.Request().Context(), item); err != nil {
		return api.iamStorageError(ctx, err)
	}
	api.audit(ctx.Request(), subjectFromRequest(ctx.Request()), "iam.group-membership.create", "success")
	return ctx.JSON(http.StatusCreated, item)
}

func (api *readAPI) removeGroupMembership(ctx *echo.Context) error {
	organizationID, groupID := ctx.Param("organizationID"), ctx.Param("groupID")
	if !api.authorizePlatformOrOrganization(ctx, "platform.identity.users.manage", "org.groups.manage", organizationID) {
		return nil
	}
	group, err := api.status.Groups().Get(ctx.Request().Context(), groupID)
	if err != nil || group.OrganizationID != organizationID || !validChangeReason(ctx.Request().Header.Get("X-KubeLoop-Reason")) {
		return api.invalidIAMMutation(ctx)
	}
	if err := api.status.Groups().RemoveMember(ctx.Request().Context(), groupID, ctx.Param("identityID")); err != nil {
		return api.iamStorageError(ctx, err)
	}
	api.audit(ctx.Request(), subjectFromRequest(ctx.Request()), "iam.group-membership.delete", "success")
	return ctx.NoContent(http.StatusNoContent)
}

func (api *readAPI) listGroupNamespaces(ctx *echo.Context) error {
	organizationID, groupID := ctx.Param("organizationID"), ctx.Param("groupID")
	if !api.authorizePlatformOrOrganization(ctx, "platform.identity.identities.read", "org.groups.read", organizationID) {
		return nil
	}
	group, err := api.status.Groups().Get(ctx.Request().Context(), groupID)
	if err != nil || group.OrganizationID != organizationID {
		return api.iamStorageError(ctx, storage.ErrNotFound)
	}
	items, err := api.status.Groups().ListNamespaces(ctx.Request().Context(), groupID)
	if err != nil {
		return api.iamStorageError(ctx, err)
	}
	return ctx.JSON(http.StatusOK, map[string]any{"items": items, "allNamespaces": group.System})
}

func (api *readAPI) addGroupNamespace(ctx *echo.Context) error {
	organizationID, groupID := ctx.Param("organizationID"), ctx.Param("groupID")
	if !api.authorizePlatformOrOrganization(ctx, "platform.identity.users.manage", "org.groups.manage", organizationID) {
		return nil
	}
	var input struct {
		Namespace string `json:"namespace"`
		Reason    string `json:"reason"`
	}
	if !decodePolicyJSON(ctx.Response(), ctx.Request(), &input) || !validChangeReason(input.Reason) {
		return api.invalidIAMMutation(ctx)
	}
	group, err := api.status.Groups().Get(ctx.Request().Context(), groupID)
	if err != nil || group.OrganizationID != organizationID || group.System {
		return api.iamStorageError(ctx, storage.ErrNotFound)
	}
	item := storage.GroupNamespace{GroupID: groupID, Namespace: input.Namespace, CreatedAt: time.Now().UTC()}
	if err := api.status.Groups().PutNamespace(ctx.Request().Context(), item); err != nil {
		return api.iamStorageError(ctx, err)
	}
	if api.authorizationReloader != nil {
		_ = api.authorizationReloader.Load(ctx.Request().Context())
	}
	api.audit(ctx.Request(), subjectFromRequest(ctx.Request()), "iam.group-namespace.create", "success")
	return ctx.JSON(http.StatusCreated, item)
}

func (api *readAPI) removeGroupNamespace(ctx *echo.Context) error {
	organizationID, groupID := ctx.Param("organizationID"), ctx.Param("groupID")
	if !api.authorizePlatformOrOrganization(ctx, "platform.identity.users.manage", "org.groups.manage", organizationID) {
		return nil
	}
	group, err := api.status.Groups().Get(ctx.Request().Context(), groupID)
	if err != nil || group.OrganizationID != organizationID || group.System || !validChangeReason(ctx.Request().Header.Get("X-KubeLoop-Reason")) {
		return api.invalidIAMMutation(ctx)
	}
	if err := api.status.Groups().DeleteNamespace(ctx.Request().Context(), groupID, ctx.Param("namespace")); err != nil {
		return api.iamStorageError(ctx, err)
	}
	if api.authorizationReloader != nil {
		_ = api.authorizationReloader.Load(ctx.Request().Context())
	}
	api.audit(ctx.Request(), subjectFromRequest(ctx.Request()), "iam.group-namespace.delete", "success")
	return ctx.NoContent(http.StatusNoContent)
}

func (api *readAPI) identityIsOrganizationMember(ctx *echo.Context, organizationID, identityID string) bool {
	items, err := api.status.Organizations().ListMembers(ctx.Request().Context(), organizationID, storage.MaximumManagementPageFetch)
	return err == nil && slices.ContainsFunc(items, func(item storage.OrganizationMembership) bool {
		return item.IdentityID == identityID && item.Status == "active"
	})
}

func (api *readAPI) invalidIAMMutation(ctx *echo.Context) error {
	writeError(ctx.Response(), http.StatusBadRequest, "invalid_request", "structured request and an 8-512 character reason are required", requestID(ctx.Request()))
	return nil
}

func (api *readAPI) iamStorageError(ctx *echo.Context, err error) error {
	status, code, message := http.StatusBadRequest, "invalid_request", "IAM request is invalid"
	if errors.Is(err, storage.ErrNotFound) {
		status, code, message = http.StatusNotFound, "not_found", "IAM resource was not found"
	} else if errors.Is(err, storage.ErrConflict) {
		status, code, message = http.StatusConflict, "conflict", "IAM resource conflicts with existing state"
	}
	writeError(ctx.Response(), status, code, message, requestID(ctx.Request()))
	return nil
}

func iamETag(updatedAt time.Time) string { return fmt.Sprintf(`"%x"`, updatedAt.UTC().UnixNano()) }

func requireIAMETag(ctx *echo.Context, updatedAt time.Time) bool {
	want := iamETag(updatedAt)
	if strings.TrimSpace(ctx.Request().Header.Get("If-Match")) == want {
		return true
	}
	ctx.Response().Header().Set("ETag", want)
	writeError(ctx.Response(), http.StatusPreconditionFailed, "etag_mismatch", "resource changed; reload before updating", requestID(ctx.Request()))
	return false
}
