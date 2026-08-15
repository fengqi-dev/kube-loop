package httpapi

import (
	"errors"
	"mime"
	"net/http"
	"time"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authorization"
	adminlocaluser "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/localuser"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/labstack/echo/v5"
)

func (api *readAPI) localUserRoutes(group *echo.Group) {
	group.GET("/users/me", api.currentLocalUser)
	group.GET("/users", api.listLocalUsers, api.permission(adminauthorization.ResourceUser, adminauthorization.OperationList))
	group.POST("/users", api.createLocalUser, api.permission(adminauthorization.ResourceUser, adminauthorization.OperationCreate))
	group.PATCH("/users/:identityID/status", api.updateLocalUserStatus, api.permission(adminauthorization.ResourceUser, adminauthorization.OperationUpdate))
	group.PUT("/users/:identityID/password", api.resetLocalUserPassword, api.permission(adminauthorization.ResourceUser, adminauthorization.OperationUpdate))
}

func (api *readAPI) currentLocalUser(ctx *echo.Context) error {
	user, err := api.localUsers.Get(ctx.Request().Context(), subjectFromRequest(ctx.Request()).ID)
	if err != nil {
		writeError(ctx.Response(), http.StatusNotFound, "not_found", "local user was not found", requestID(ctx.Request()))
		return nil
	}
	return ctx.JSON(http.StatusOK, user)
}

func (api *readAPI) listLocalUsers(ctx *echo.Context) error {
	users, err := api.localUsers.List(ctx.Request().Context())
	if err != nil {
		api.audit(ctx.Request(), subjectFromRequest(ctx.Request()), "admin.user/list", "failure")
		writeError(ctx.Response(), http.StatusServiceUnavailable, "unavailable", "local users are unavailable", requestID(ctx.Request()))
		return nil
	}
	api.audit(ctx.Request(), subjectFromRequest(ctx.Request()), "admin.user/list", "success")
	return ctx.JSON(http.StatusOK, map[string]any{"items": users})
}

func (api *readAPI) createLocalUser(ctx *echo.Context) error {
	var input struct {
		Username, Password, DisplayName, Email string
		GroupID                                string `json:"groupId"`
	}
	if bindJSON(ctx, &input) != nil {
		writeError(ctx.Response(), http.StatusBadRequest, "invalid_request", "invalid request", requestID(ctx.Request()))
		return nil
	}
	password := []byte(input.Password)
	input.Password = ""
	group, err := api.status.Groups().Get(ctx.Request().Context(), input.GroupID)
	if err != nil {
		clear(password)
		writeError(ctx.Response(), http.StatusBadRequest, "invalid_request", "a valid group is required", requestID(ctx.Request()))
		return nil
	}
	var user adminlocaluser.User
	err = api.oauthTransactions.WithinTransaction(ctx.Request().Context(), func(repositories storage.Repositories) error {
		created, createErr := api.localUsers.CreateWithRepositories(ctx.Request().Context(), repositories, adminlocaluser.CreateRequest{
			Username: input.Username, Password: password, DisplayName: input.DisplayName, Email: input.Email,
		})
		if createErr != nil {
			return createErr
		}
		user = created
		now := time.Now().UTC()
		if createErr = repositories.Organizations().AddMember(ctx.Request().Context(), storage.OrganizationMembership{
			OrganizationID: group.OrganizationID, IdentityID: user.IdentityID, Status: "active", CreatedAt: now, UpdatedAt: now,
		}); createErr != nil {
			return createErr
		}
		return repositories.Groups().AddMember(ctx.Request().Context(), storage.GroupMembership{
			GroupID: group.ID, IdentityID: user.IdentityID, SourceType: "manual", CreatedAt: now,
		})
	})
	clear(password)
	if err != nil {
		api.audit(ctx.Request(), subjectFromRequest(ctx.Request()), "admin.user/create", "failure")
		writeError(ctx.Response(), http.StatusBadRequest, "invalid_request", "local user could not be created", requestID(ctx.Request()))
		return nil
	}
	api.audit(ctx.Request(), subjectFromRequest(ctx.Request()), "admin.user/create", "success")
	return ctx.JSON(http.StatusCreated, user)
}

func (api *readAPI) updateLocalUserStatus(ctx *echo.Context) error {
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if bindJSON(ctx, &input) != nil || input.Enabled == nil ||
		(!*input.Enabled && api.wouldRemoveLastAdministrator(ctx.Request(), ctx.Param("identityID"))) ||
		api.localUsers.SetEnabled(ctx.Request().Context(), ctx.Param("identityID"), *input.Enabled) != nil {
		writeError(ctx.Response(), http.StatusBadRequest, "invalid_request", "local user status could not be updated", requestID(ctx.Request()))
		return nil
	}
	api.audit(ctx.Request(), subjectFromRequest(ctx.Request()), "admin.user/update", "success")
	return ctx.NoContent(http.StatusNoContent)
}

func (api *readAPI) wouldRemoveLastAdministrator(request *http.Request, identityID string) bool {
	organizations, err := api.status.Organizations().List(request.Context(), 2)
	if err != nil || len(organizations) != 1 {
		return true
	}
	groups, err := api.status.Groups().List(request.Context(), organizations[0].ID, storage.MaximumManagementPageFetch)
	if err != nil {
		return true
	}
	users, err := api.localUsers.List(request.Context())
	if err != nil {
		return true
	}
	enabled := make(map[string]bool, len(users))
	for _, user := range users {
		enabled[user.IdentityID] = user.Enabled
	}
	targetIsAdministrator := false
	for _, group := range groups {
		if !group.System {
			continue
		}
		members, listErr := api.status.Groups().ListMembers(request.Context(), group.ID, storage.MaximumManagementPageFetch)
		if listErr != nil {
			return true
		}
		for _, member := range members {
			if member.IdentityID == identityID {
				targetIsAdministrator = true
			} else if enabled[member.IdentityID] {
				return false
			}
		}
	}
	return targetIsAdministrator
}

func (api *readAPI) resetLocalUserPassword(ctx *echo.Context) error {
	var input struct {
		Password string `json:"password"`
	}
	if bindJSON(ctx, &input) != nil {
		writeError(ctx.Response(), http.StatusBadRequest, "invalid_request", "invalid request", requestID(ctx.Request()))
		return nil
	}
	password := []byte(input.Password)
	input.Password = ""
	err := api.localUsers.SetPassword(ctx.Request().Context(), ctx.Param("identityID"), password)
	clear(password)
	if err != nil {
		writeError(ctx.Response(), http.StatusBadRequest, "invalid_request", "local user password could not be updated", requestID(ctx.Request()))
		return nil
	}
	api.audit(ctx.Request(), subjectFromRequest(ctx.Request()), "admin.user.password.reset", "success")
	return ctx.NoContent(http.StatusNoContent)
}

func bindJSON(ctx *echo.Context, target any) error {
	mediaType, _, err := mime.ParseMediaType(ctx.Request().Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return errors.New("application/json is required")
	}
	return ctx.Bind(target)
}
