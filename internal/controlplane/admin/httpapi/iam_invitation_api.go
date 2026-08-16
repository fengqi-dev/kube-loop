package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	adminlocaluser "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/localuser"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
)

func (api *readAPI) listInvitations(ctx *echo.Context) error {
	organizationID := ctx.Param("organizationID")
	if !api.authorizePlatformOrOrganization(ctx, "platform.identity.users.read", "org.identities.read", organizationID) {
		return nil
	}
	items, err := api.status.Invitations().List(ctx.Request().Context(), organizationID, 100)
	if err != nil {
		return api.iamStorageError(ctx, err)
	}
	return ctx.JSON(http.StatusOK, map[string]any{"items": items})
}

func (api *readAPI) createInvitation(ctx *echo.Context) error {
	organizationID := ctx.Param("organizationID")
	if !api.authorizePlatformOrOrganization(ctx, "platform.identity.users.manage", "org.invitations.manage", organizationID) {
		return nil
	}
	var input struct {
		Email   string `json:"email"`
		GroupID string `json:"groupId"`
		Reason  string `json:"reason"`
	}
	if !decodePolicyJSON(ctx.Response(), ctx.Request(), &input) || !validChangeReason(input.Reason) {
		return api.invalidIAMMutation(ctx)
	}
	group, err := api.status.Groups().Get(ctx.Request().Context(), input.GroupID)
	if err != nil || group.OrganizationID != organizationID || group.System {
		return api.invalidIAMMutation(ctx)
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return api.iamStorageError(ctx, err)
	}
	plaintext := base64.RawURLEncoding.EncodeToString(raw)
	clear(raw)
	digest := sha256.Sum256([]byte(plaintext))
	now := time.Now().UTC()
	item := storage.Invitation{ID: uuid.NewString(), OrganizationID: organizationID, Email: strings.ToLower(strings.TrimSpace(input.Email)),
		GroupID: input.GroupID, TokenHash: digest[:], Status: "pending", InvitedBy: subjectFromRequest(ctx.Request()).ID,
		CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour)}
	if err := api.status.Invitations().Create(ctx.Request().Context(), item); err != nil {
		return api.iamStorageError(ctx, err)
	}
	api.audit(ctx.Request(), subjectFromRequest(ctx.Request()), "iam.invitation.create", "success")
	return ctx.JSON(http.StatusCreated, map[string]any{"id": item.ID, "organizationId": organizationID,
		"email": item.Email, "groupId": item.GroupID, "expiresAt": item.ExpiresAt, "invitationToken": plaintext})
}

func (api *readAPI) revokeInvitation(ctx *echo.Context) error {
	organizationID := ctx.Param("organizationID")
	if !api.authorizePlatformOrOrganization(ctx, "platform.identity.users.manage", "org.invitations.manage", organizationID) {
		return nil
	}
	if !validChangeReason(ctx.Request().Header.Get("X-KubeLoop-Reason")) {
		return api.invalidIAMMutation(ctx)
	}
	if err := api.status.Invitations().Revoke(ctx.Request().Context(), ctx.Param("invitationID"), time.Now().UTC()); err != nil {
		return api.iamStorageError(ctx, err)
	}
	api.audit(ctx.Request(), subjectFromRequest(ctx.Request()), "iam.invitation.revoke", "success")
	return ctx.NoContent(http.StatusNoContent)
}

func (api *readAPI) acceptInvitation(ctx *echo.Context) error {
	writer, request := ctx.Response(), ctx.Request()
	requestID := ensureRequestID(writer, request)
	var input struct {
		Token       string `json:"token"`
		Username    string `json:"username"`
		Password    string `json:"password"`
		DisplayName string `json:"displayName"`
	}
	if !decodePolicyJSON(writer, request, &input) {
		return nil
	}
	digest := sha256.Sum256([]byte(strings.TrimSpace(input.Token)))
	invitation, err := api.status.Invitations().GetByTokenHash(request.Context(), digest[:], time.Now().UTC())
	if err != nil {
		writeError(writer, http.StatusUnauthorized, "invalid_invitation", "invitation is invalid or expired", requestID)
		return nil
	}
	password := []byte(input.Password)
	input.Password = ""
	var identityID string
	err = api.oauthTransactions.WithinTransaction(request.Context(), func(repositories storage.Repositories) error {
		user, createErr := api.localUsers.CreateWithRepositories(request.Context(), repositories, adminlocaluser.CreateRequest{
			Username: input.Username, Password: password, DisplayName: input.DisplayName, Email: invitation.Email,
		})
		if createErr != nil {
			return createErr
		}
		identityID = user.IdentityID
		now := time.Now().UTC()
		if createErr = repositories.Organizations().AddMember(request.Context(), storage.OrganizationMembership{
			OrganizationID: invitation.OrganizationID, IdentityID: identityID, Status: "active", CreatedAt: now, UpdatedAt: now,
		}); createErr != nil {
			return createErr
		}
		if createErr = repositories.Groups().AddMember(request.Context(), storage.GroupMembership{
			GroupID: invitation.GroupID, IdentityID: identityID, CreatedAt: now,
		}); createErr != nil {
			return createErr
		}
		return repositories.Invitations().Accept(request.Context(), invitation.ID, identityID, now)
	})
	clear(password)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, storage.ErrNotFound) {
			status = http.StatusUnauthorized
		}
		writeError(writer, status, "invalid_invitation", "invitation could not be accepted", requestID)
		return nil
	}
	if err := api.authorizationReloader.Load(request.Context()); err != nil {
		writeError(writer, http.StatusServiceUnavailable, "unavailable", "IAM policy reload failed", requestID)
		return nil
	}
	return ctx.JSON(http.StatusCreated, map[string]any{"identityId": identityID, "organizationId": invitation.OrganizationID,
		"groupId": invitation.GroupID})
}
