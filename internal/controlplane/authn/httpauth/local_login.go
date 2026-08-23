package httpauth

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"
)

func (routes *Routes) localLogin(ctx *echo.Context) error {
	form, ok := routes.bindForm(ctx)
	if !ok {
		return nil
	}
	password := []byte(form.Get("password"))
	defer clear(password)
	requestID := strings.TrimSpace(ctx.Request().Header.Get("X-Request-ID"))
	if requestID == "" {
		requestID = strings.TrimSpace(ctx.Response().Header().Get("X-Request-ID"))
	}
	if form.Get("decision") == "cancel" {
		if err := routes.fosite.CancelAuthorization(
			ctx.Response(), ctx.Request(), form.Get(queryTransaction), form.Get(queryCSRF),
		); err != nil {
			return writeBrowserError(ctx)
		}
		return nil
	}
	identity, ok := routes.existingIdentity(ctx)
	if !ok || form.Get("session") != "true" {
		var err error
		identity, err = routes.fosite.AuthenticateLocal(
			ctx.Request().Context(), form.Get("username"), password, requestID,
		)
		if err != nil {
			if target := browserLoginErrorURL(form, "authentication_failed"); target != "" {
				return ctx.Redirect(http.StatusSeeOther, target)
			}
			return writeBrowserError(ctx)
		}
		sessionToken, sessionErr := routes.fosite.CreateBrowserSession(
			ctx.Request().Context(), identity, browserSessionTTL,
		)
		if sessionErr != nil {
			return writeBrowserError(ctx)
		}
		cookieName, secure := routes.browserSessionCookie()
		//nolint:gosec // Secure follows the validated issuer URL; local loopback OAuth intentionally permits HTTP.
		ctx.SetCookie(&http.Cookie{
			Name: cookieName, Value: sessionToken, Path: "/", Secure: secure,
			HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: int(browserSessionTTL.Seconds()),
		})
	}
	if err := routes.fosite.CompleteAuthorization(
		ctx.Response(), ctx.Request(), form.Get(queryTransaction), form.Get(queryCSRF),
		identity, form.Get("decision") == "allow",
	); err != nil {
		return writeBrowserError(ctx)
	}
	return nil
}
