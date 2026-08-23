package httpauth

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v5"
)

func (routes *Routes) logout(ctx *echo.Context) error {
	request, writer := ctx.Request(), ctx.Response()
	if request.Header.Get("Origin") != routes.issuer || request.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return routes.oauthError(
			ctx, http.StatusUnauthorized, "invalid_request", "logout request was rejected",
		)
	}
	cookieName, secure := routes.browserSessionCookie()
	cookie, err := request.Cookie(cookieName)
	if err == nil {
		if err := routes.fosite.RevokeBrowserSession(request.Context(), cookie.Value); err != nil {
			return routes.oauthError(
				ctx, http.StatusServiceUnavailable, "temporarily_unavailable", "logout could not be completed",
			)
		}
	}
	//nolint:gosec // Secure follows the validated issuer URL; this only expires the existing browser cookie.
	http.SetCookie(writer, &http.Cookie{
		Name: cookieName, Value: "", Path: "/", Secure: secure,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(1, 0).UTC(),
	})
	return ctx.NoContent(http.StatusNoContent)
}
