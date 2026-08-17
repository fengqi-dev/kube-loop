package httpapi

import (
	"net/http"

	adminlocaluser "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/localuser"
	"github.com/labstack/echo/v5"
)

func (api *readAPI) localUserRoutes(group *echo.Group) {
	group.GET("/users/me", api.currentLocalUser)
	group.GET("/users", api.listLocalUsers)
	group.POST("/users", api.createLocalUser)
	group.PATCH("/users/:identityID/status", api.updateLocalUserStatus)
	group.PUT("/users/:identityID/password", api.resetLocalUserPassword)
}

func (api *readAPI) currentLocalUser(ctx *echo.Context) error {
	user, err := api.localUsers.Get(ctx.Request().Context(), subjectFromRequest(ctx.Request()).ID)
	if err != nil {
		return writeError(ctx, http.StatusNotFound, "not_found", "local user was not found", requestID(ctx.Request()))
	}
	return ctx.JSON(http.StatusOK, user)
}

func (api *readAPI) listLocalUsers(ctx *echo.Context) error {
	users, err := api.localUsers.List(ctx.Request().Context())
	if err != nil {
		api.audit(ctx.Request(), subjectFromRequest(ctx.Request()), "admin.user/list", "failure")
		return writeError(ctx, http.StatusServiceUnavailable, "unavailable", "local users are unavailable", requestID(ctx.Request()))
	}
	api.audit(ctx.Request(), subjectFromRequest(ctx.Request()), "admin.user/list", "success")
	return ctx.JSON(http.StatusOK, map[string]any{"items": users})
}

func (api *readAPI) createLocalUser(ctx *echo.Context) error {
	var input struct {
		Username, Password, DisplayName, Email string
	}
	if responseErr := bindJSON(ctx, &input); responseErr != nil {
		return responseErr.write(ctx)
	}
	password := []byte(input.Password)
	input.Password = ""
	user, err := api.localUsers.Create(ctx.Request().Context(), adminlocaluser.CreateRequest{
		Username: input.Username, Password: password, DisplayName: input.DisplayName, Email: input.Email,
	})
	clear(password)
	if err != nil {
		api.audit(ctx.Request(), subjectFromRequest(ctx.Request()), "admin.user/create", "failure")
		return writeError(ctx, http.StatusBadRequest, "invalid_request", "local user could not be created", requestID(ctx.Request()))
	}
	api.audit(ctx.Request(), subjectFromRequest(ctx.Request()), "admin.user/create", "success")
	return ctx.JSON(http.StatusCreated, user)
}

func (api *readAPI) updateLocalUserStatus(ctx *echo.Context) error {
	var input struct {
		Enabled *bool  `json:"enabled"`
		Reason  string `json:"reason"`
	}
	if responseErr := bindJSON(ctx, &input); responseErr != nil {
		return responseErr.write(ctx)
	}
	if input.Enabled == nil || !validChangeReason(input.Reason) ||
		api.localUsers.SetEnabled(ctx.Request().Context(), ctx.Param("identityID"), *input.Enabled) != nil {
		return writeError(ctx, http.StatusBadRequest, "invalid_request", "local user status could not be updated", requestID(ctx.Request()))
	}
	api.audit(ctx.Request(), subjectFromRequest(ctx.Request()), "admin.user/update", "success")
	return ctx.NoContent(http.StatusNoContent)
}

func (api *readAPI) resetLocalUserPassword(ctx *echo.Context) error {
	var input struct {
		Password string `json:"password"`
	}
	if responseErr := bindJSON(ctx, &input); responseErr != nil {
		return responseErr.write(ctx)
	}
	password := []byte(input.Password)
	input.Password = ""
	err := api.localUsers.SetPassword(ctx.Request().Context(), ctx.Param("identityID"), password)
	clear(password)
	if err != nil {
		return writeError(ctx, http.StatusBadRequest, "invalid_request", "local user password could not be updated", requestID(ctx.Request()))
	}
	api.audit(ctx.Request(), subjectFromRequest(ctx.Request()), "admin.user.password.reset", "success")
	return ctx.NoContent(http.StatusNoContent)
}
