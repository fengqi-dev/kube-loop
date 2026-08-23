package httpauth

import (
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn/oauthserver"
)

func (routes *Routes) token(ctx *echo.Context) error {
	routes.fosite.Token(ctx.Response(), ctx.Request())
	return nil
}

func (routes *Routes) revoke(ctx *echo.Context) error {
	routes.fosite.Revoke(ctx.Response(), ctx.Request())
	return nil
}

func (routes *Routes) jwks(ctx *echo.Context) error {
	ctx.Response().Header().Set("Cache-Control", "public, max-age=300")
	return ctx.JSON(http.StatusOK, routes.fosite.KeySet())
}

func (routes *Routes) userInfo(ctx *echo.Context) error {
	session, _, err := routes.fosite.IntrospectAccessToken(
		ctx.Request().Context(),
		oauthserver.BearerToken(ctx.Request()),
	)
	if err != nil || session.Machine {
		ctx.Response().Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
		return routes.oauthError(
			ctx, http.StatusUnauthorized, "invalid_token", "access token was rejected",
		)
	}
	return ctx.JSON(http.StatusOK, map[string]any{
		"sub": session.IdentityID, "name": session.DisplayName, "email": session.Email,
	})
}
