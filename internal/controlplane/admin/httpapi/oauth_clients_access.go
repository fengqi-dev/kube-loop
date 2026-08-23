package httpapi

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v5"
)

func (api *readAPI) setOAuthClientEnabled(ctx *echo.Context) error {
	var input struct {
		Enabled bool   `json:"enabled"`
		Reason  string `json:"reason"`
	}
	if responseErr := bindJSON(ctx, &input); responseErr != nil {
		return responseErr.write(ctx)
	}
	if !validChangeReason(input.Reason) {
		return api.invalidIAMMutation(ctx)
	}
	client, err := api.oauthRepositories.OAuthClients().
		Get(ctx.Request().Context(), ctx.Param("clientID"))
	if err != nil {
		return api.oauthClientError(ctx, err)
	}
	if responseErr := requireIAMETag(ctx, client.UpdatedAt); responseErr != nil {
		return responseErr.write(ctx)
	}
	client.Enabled = input.Enabled
	client.UpdatedAt = time.Now().UTC()
	if err := api.oauthRepositories.OAuthClients().Update(ctx.Request().Context(), client); err != nil {
		return api.oauthClientError(ctx, err)
	}
	stored, _ := api.oauthRepositories.OAuthClients().
		Get(ctx.Request().Context(), client.ID)
	api.audit(
		ctx.Request(),
		subjectFromRequest(ctx.Request()),
		"iam.oauth-client.enabled.update",
		"success",
	)
	ctx.Response().Header().Set("ETag", iamETag(stored.UpdatedAt))
	return ctx.JSON(http.StatusOK, oauthClientDocument(stored))
}

func (api *readAPI) revokeOAuthConsent(ctx *echo.Context) error {
	_, err := api.oauthRepositories.OAuthClients().
		Get(ctx.Request().Context(), ctx.Param("clientID"))
	if err != nil {
		return api.oauthClientError(ctx, err)
	}
	if !validChangeReason(ctx.Request().Header.Get("X-Kubeloop-Reason")) {
		return api.invalidIAMMutation(ctx)
	}
	if err := api.oauthRepositories.OAuthConsents().RevokeClient(
		ctx.Request().Context(), ctx.Param("identityID"), ctx.Param("clientID"),
	); err != nil {
		return api.oauthClientError(ctx, err)
	}
	api.audit(
		ctx.Request(),
		subjectFromRequest(ctx.Request()),
		"iam.oauth-consent.revoke",
		"success",
	)
	return ctx.NoContent(http.StatusNoContent)
}
