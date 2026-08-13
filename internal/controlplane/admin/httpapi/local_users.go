package httpapi

import (
	"errors"
	"mime"
	"net/http"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authorization"
	adminlocaluser "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/localuser"
	"github.com/labstack/echo/v5"
)

func (api *readAPI) localUserRoutes(group *echo.Group) {
	group.GET("/users/me", api.currentLocalUser)
	group.POST("/users/me/mfa/totp/start", api.startTOTP)
	group.POST("/users/me/mfa/totp/confirm", api.confirmTOTP)
	group.POST("/users/me/mfa/recovery-codes", api.regenerateRecoveryCodes)
	group.DELETE("/users/me/mfa/totp", api.disableTOTP)
	group.GET("/users", api.listLocalUsers, api.permission(adminauthorization.ResourceUser, adminauthorization.OperationList))
	group.POST("/users", api.createLocalUser, api.permission(adminauthorization.ResourceUser, adminauthorization.OperationCreate))
	group.PATCH("/users/:principalID/status", api.updateLocalUserStatus, api.permission(adminauthorization.ResourceUser, adminauthorization.OperationUpdate))
	group.PUT("/users/:principalID/password", api.resetLocalUserPassword, api.permission(adminauthorization.ResourceUser, adminauthorization.OperationUpdate))
}

func (api *readAPI) currentLocalUser(ctx *echo.Context) error {
	subject := subjectFromRequest(ctx.Request())
	user, err := api.localUsers.Get(ctx.Request().Context(), subject.ID)
	if err == nil {
		api.audit(ctx.Request(), subject, "admin.user.self/read", "success")
		return ctx.JSON(http.StatusOK, user)
	}
	api.audit(ctx.Request(), subject, "admin.user.self/read", "failure")
	writeError(ctx.Response(), http.StatusNotFound, "not_found", "local user was not found", requestID(ctx.Request()))
	return nil
}

func (api *readAPI) startTOTP(ctx *echo.Context) error {
	enrollment, err := api.localUsers.StartTOTP(ctx.Request().Context(), subjectFromRequest(ctx.Request()).ID)
	if err != nil {
		api.audit(ctx.Request(), subjectFromRequest(ctx.Request()), "admin.user.mfa.enroll", "failure")
		writeError(ctx.Response(), http.StatusBadRequest, "invalid_request", "TOTP enrollment could not be started", requestID(ctx.Request()))
		return nil
	}
	api.audit(ctx.Request(), subjectFromRequest(ctx.Request()), "admin.user.mfa.enroll", "success")
	return ctx.JSON(http.StatusCreated, enrollment)
}

func (api *readAPI) confirmTOTP(ctx *echo.Context) error {
	var input struct {
		EnrollmentToken string `json:"enrollmentToken"`
		Code            string `json:"code"`
	}
	if bindJSON(ctx, &input) != nil {
		writeError(ctx.Response(), http.StatusBadRequest, "invalid_request", "invalid request", requestID(ctx.Request()))
		return nil
	}
	codes, err := api.localUsers.ConfirmTOTP(ctx.Request().Context(), subjectFromRequest(ctx.Request()).ID, input.EnrollmentToken, input.Code)
	if err != nil {
		api.audit(ctx.Request(), subjectFromRequest(ctx.Request()), "admin.user.mfa.confirm", "failure")
		writeError(ctx.Response(), http.StatusBadRequest, "invalid_request", "TOTP enrollment could not be confirmed", requestID(ctx.Request()))
		return nil
	}
	api.audit(ctx.Request(), subjectFromRequest(ctx.Request()), "admin.user.mfa.confirm", "success")
	return ctx.JSON(http.StatusCreated, map[string]any{"recoveryCodes": codes})
}

func (api *readAPI) regenerateRecoveryCodes(ctx *echo.Context) error {
	var input struct {
		Code string `json:"code"`
	}
	if bindJSON(ctx, &input) != nil {
		writeError(ctx.Response(), http.StatusBadRequest, "invalid_request", "invalid request", requestID(ctx.Request()))
		return nil
	}
	codes, err := api.localUsers.RegenerateRecoveryCodes(ctx.Request().Context(), subjectFromRequest(ctx.Request()).ID, input.Code)
	if err != nil {
		api.audit(ctx.Request(), subjectFromRequest(ctx.Request()), "admin.user.mfa.recovery.regenerate", "failure")
		writeError(ctx.Response(), http.StatusUnauthorized, "unauthenticated", "MFA verification failed", requestID(ctx.Request()))
		return nil
	}
	api.audit(ctx.Request(), subjectFromRequest(ctx.Request()), "admin.user.mfa.recovery.regenerate", "success")
	return ctx.JSON(http.StatusCreated, map[string]any{"recoveryCodes": codes})
}

func (api *readAPI) disableTOTP(ctx *echo.Context) error {
	var input struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if bindJSON(ctx, &input) != nil || api.localUsers.DisableTOTP(ctx.Request().Context(), subjectFromRequest(ctx.Request()).ID, input.Password, input.Code) != nil {
		api.audit(ctx.Request(), subjectFromRequest(ctx.Request()), "admin.user.mfa.disable", "failure")
		writeError(ctx.Response(), http.StatusUnauthorized, "unauthenticated", "MFA verification failed", requestID(ctx.Request()))
		return nil
	}
	api.audit(ctx.Request(), subjectFromRequest(ctx.Request()), "admin.user.mfa.disable", "success")
	return ctx.NoContent(http.StatusNoContent)
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
		Username    string `json:"username"`
		Password    string `json:"password"`
		DisplayName string `json:"displayName"`
		Email       string `json:"email"`
	}
	if bindJSON(ctx, &input) != nil {
		writeError(ctx.Response(), http.StatusBadRequest, "invalid_request", "invalid request", requestID(ctx.Request()))
		return nil
	}
	password := []byte(input.Password)
	input.Password = ""
	user, err := api.localUsers.Create(ctx.Request().Context(), adminlocaluser.CreateRequest{
		Username: input.Username, Password: password, DisplayName: input.DisplayName, Email: input.Email,
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
		(!*input.Enabled && api.wouldRemoveLastPlatformAdmin(ctx.Request(), ctx.Param("principalID"))) ||
		api.localUsers.SetEnabled(ctx.Request().Context(), ctx.Param("principalID"), *input.Enabled) != nil {
		api.audit(ctx.Request(), subjectFromRequest(ctx.Request()), "admin.user/update", "failure")
		writeError(ctx.Response(), http.StatusBadRequest, "invalid_request", "local user status could not be updated", requestID(ctx.Request()))
		return nil
	}
	api.audit(ctx.Request(), subjectFromRequest(ctx.Request()), "admin.user/update", "success")
	return ctx.NoContent(http.StatusNoContent)
}

func (api *readAPI) wouldRemoveLastPlatformAdmin(request *http.Request, principalID string) bool {
	if api.policy == nil {
		return true
	}
	policy, err := api.policy.CurrentPolicy(request.Context())
	if err != nil {
		return true
	}
	localUsers, err := api.localUsers.List(request.Context())
	if err != nil {
		return true
	}
	localEnabled := make(map[string]bool, len(localUsers))
	for _, user := range localUsers {
		localEnabled[user.PrincipalID] = user.Enabled
	}
	targetIsAdmin := false
	for _, binding := range policy.Snapshot.Bindings {
		if binding.RoleID != adminauthorization.RolePlatformAdmin {
			continue
		}
		if binding.Subject.Type == adminauthorization.SubjectGroup {
			return false
		}
		if binding.Subject.Type == adminauthorization.SubjectPrincipal {
			subject := binding.Subject.PrincipalID
			if subject == principalID {
				targetIsAdmin = true
			} else if enabled, local := localEnabled[subject]; !local || enabled {
				return false
			}
		}
	}
	return targetIsAdmin
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
	err := api.localUsers.SetPassword(ctx.Request().Context(), ctx.Param("principalID"), password)
	clear(password)
	if err != nil {
		api.audit(ctx.Request(), subjectFromRequest(ctx.Request()), "admin.user.password.reset", "failure")
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
