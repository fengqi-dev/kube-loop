package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"

	adminbootstrap "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/bootstrap"
	adminlocaluser "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/localuser"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

type completeBootstrapRequest struct {
	Token       string `json:"token"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
}

func (api *readAPI) completeBootstrap(ctx *echo.Context) error {
	request := ctx.Request()
	requestID := ensureRequestID(ctx)
	var input completeBootstrapRequest
	if responseErr := bindJSON(ctx, &input); responseErr != nil {
		return responseErr.write(ctx)
	}
	result, err := api.bootstrapService.Complete(
		request.Context(),
		adminbootstrap.CompleteRequest{
			Token: input.Token, Username: input.Username, Password: []byte(input.Password),
			DisplayName: input.DisplayName, Email: input.Email, RequestID: requestID,
		},
	)
	input.Password = ""
	if err != nil {
		status, code, message := http.StatusBadRequest, invalidRequestCode, "bootstrap request is invalid"
		switch {
		case errors.Is(err, adminbootstrap.ErrInvalidToken),
			errors.Is(err, adminbootstrap.ErrAlreadyCompleted):
			status, code, message = http.StatusUnauthorized, "invalid_bootstrap_token", "bootstrap token is invalid or expired"
		case errors.Is(err, adminlocaluser.ErrInvalidInput):
			message = "identity or password is invalid"
		case errors.Is(err, storage.ErrConflict):
			status, code, message = http.StatusConflict, "conflict", "identity already exists"
		}
		return writeError(ctx, status, code, message, requestID)
	}
	return ctx.JSON(
		http.StatusCreated,
		map[string]any{"identity": map[string]string{
			"id":          result.Identity.IdentityID,
			"displayName": result.Identity.DisplayName,
			emailField:    result.Identity.Email,
		}},
	)
}

func (api *readAPI) bootstrap(ctx *echo.Context) error {
	request := ctx.Request()
	subject := subjectFromRequest(request)
	stored, ok := request.Context().Value(sessionContextKey).(storage.AdminSession)
	if !ok {
		return writeError(
			ctx,
			http.StatusUnauthorized,
			"unauthenticated",
			"management authentication failed",
			requestID(request),
		)
	}
	identity, err := api.repositories.Identities().
		GetByID(request.Context(), subject.ID)
	if err != nil {
		return writeError(
			ctx,
			http.StatusUnauthorized,
			"unauthenticated",
			"management identity is unavailable",
			requestID(request),
		)
	}
	api.audit(request, subject, "admin.bootstrap/read", "success")
	return ctx.JSON(http.StatusOK, map[string]any{
		"identity": map[string]any{
			"id":          identity.ID,
			"displayName": identity.DisplayName, emailField: identity.PrimaryEmail, "type": identity.Type,
		},
		"session": map[string]any{
			authenticationTypeField: stored.AuthenticationType,
			"createdAt":             stored.CreatedAt,
			"lastSeenAt":            stored.LastSeenAt,
			"idleExpiresAt":         stored.IdleExpiresAt,
			"absoluteExpiresAt":     stored.AbsoluteExpiresAt,
		},
		"generatedAt": time.Now().UTC(),
	})
}
