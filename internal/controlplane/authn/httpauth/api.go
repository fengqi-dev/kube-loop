package httpauth

import (
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn/oauthserver"
)

const (
	browserSessionTTL = 12 * time.Hour
)

func (routes *Routes) authorize(ctx *echo.Context) error {
	challenge, err := routes.fosite.BeginAuthorization(
		ctx.Request().Context(),
		ctx.Request(),
	)
	if err != nil {
		return routes.oauthError(
			ctx,
			http.StatusBadRequest,
			"invalid_request",
			"authorization request was rejected",
		)
	}
	identity, hasSession := routes.existingIdentity(ctx)
	prompt := strings.Fields(ctx.QueryParam("prompt"))
	if slices.Contains(prompt, "login") {
		hasSession = false
	}
	if hasSession && sessionTooOld(ctx, ctx.QueryParam("max_age")) {
		hasSession = false
	}
	forceConsent := slices.Contains(prompt, "consent")
	if hasSession && forceConsent && slices.Contains(prompt, "none") {
		return routes.completeAuthorizationError(
			ctx,
			challenge,
			"consent_required",
		)
	}
	if hasSession && !forceConsent {
		required, consentErr := routes.fosite.ConsentRequired(
			ctx.Request().Context(),
			challenge,
			identity.Identity.ID,
		)
		if consentErr == nil && !required {
			_ = routes.fosite.CompleteAuthorization(
				ctx.Response(),
				ctx.Request(),
				challenge.Transaction,
				challenge.CSRF,
				identity,
				false,
			)
			return nil
		}
		if slices.Contains(prompt, "none") && consentErr == nil && required {
			return routes.completeAuthorizationError(
				ctx,
				challenge,
				"consent_required",
			)
		}
	}
	if slices.Contains(prompt, "none") {
		return routes.completeAuthorizationError(
			ctx,
			challenge,
			"login_required",
		)
	}
	uiQuery := url.Values{
		queryTransaction: {challenge.Transaction},
		queryCSRF:        {challenge.CSRF},
		"client":         {challenge.Client.Name},
		"session":        {strconv.FormatBool(hasSession)},
		"consent":        {strconv.FormatBool(!challenge.Trusted)},
		"scope":          {strings.Join(challenge.Scopes, " ")},
	}
	return ctx.Redirect(http.StatusSeeOther, oauthPath+"/ui/?"+uiQuery.Encode())
}

func (routes *Routes) existingIdentity(
	ctx *echo.Context,
) (oauthserver.BrowserIdentity, bool) {
	cookieName, _ := routes.browserSessionCookie()
	cookie, err := ctx.Request().Cookie(cookieName)
	if err == nil && strings.TrimSpace(cookie.Value) != "" {
		identity, identityErr := routes.fosite.BrowserIdentity(
			ctx.Request().Context(),
			cookie.Value,
		)
		if identityErr == nil {
			ctx.Set("oauth.browser.auth_time", identity.AuthTime)
			return identity, true
		}
	}
	return oauthserver.BrowserIdentity{}, false
}

func sessionTooOld(ctx *echo.Context, raw string) bool {
	if strings.TrimSpace(raw) == "" {
		return false
	}
	seconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || seconds < 0 {
		return true
	}
	authTime, ok := ctx.Get("oauth.browser.auth_time").(time.Time)
	return !ok || time.Since(authTime) > time.Duration(seconds)*time.Second
}

func (routes *Routes) completeAuthorizationError(
	ctx *echo.Context,
	challenge oauthserver.AuthorizationChallenge,
	code string,
) error {
	if code == "login_required" || code == "consent_required" {
		if err := routes.fosite.DenyAuthorization(
			ctx.Response(), ctx.Request(), challenge.Transaction, challenge.CSRF, code,
		); err != nil {
			return routes.oauthError(
				ctx,
				http.StatusBadRequest,
				"invalid_request",
				"authorization request was rejected",
			)
		}
		return nil
	}
	return routes.oauthError(
		ctx,
		http.StatusBadRequest,
		code,
		"authorization request was rejected",
	)
}

func (routes *Routes) localLogin(ctx *echo.Context) error {
	form, ok := routes.bindForm(ctx)
	if !ok {
		return nil
	}
	password := []byte(form.Get("password"))
	defer clear(password)
	requestID := strings.TrimSpace(ctx.Request().Header.Get("X-Request-ID"))
	if requestID == "" {
		requestID = strings.TrimSpace(
			ctx.Response().Header().Get("X-Request-ID"),
		)
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
			ctx.Request().Context(),
			form.Get("username"),
			password,
			requestID,
		)
		if err != nil {
			if target := browserLoginErrorURL(form, "authentication_failed"); target != "" {
				return ctx.Redirect(http.StatusSeeOther, target)
			}
			return writeBrowserError(ctx)
		}
		sessionToken, sessionErr := routes.fosite.CreateBrowserSession(
			ctx.Request().Context(),
			identity,
			browserSessionTTL,
		)
		if sessionErr != nil {
			return writeBrowserError(ctx)
		}
		cookieName, secure := routes.browserSessionCookie()
		//nolint:gosec // Secure follows the validated issuer URL; local loopback OAuth intentionally permits HTTP.
		ctx.SetCookie(
			&http.Cookie{
				Name:     cookieName,
				Value:    sessionToken,
				Path:     "/",
				Secure:   secure,
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
				MaxAge:   int(browserSessionTTL.Seconds()),
			},
		)
	}
	if err := routes.fosite.CompleteAuthorization(
		ctx.Response(), ctx.Request(), form.Get(queryTransaction), form.Get(queryCSRF),
		identity, form.Get("decision") == "allow",
	); err != nil {
		return writeBrowserError(ctx)
	}
	return nil
}

func (routes *Routes) token(ctx *echo.Context) error {
	routes.fosite.Token(ctx.Response(), ctx.Request())
	return nil
}

func (routes *Routes) revoke(ctx *echo.Context) error {
	routes.fosite.Revoke(ctx.Response(), ctx.Request())
	return nil
}

func (routes *Routes) logout(ctx *echo.Context) error {
	request, writer := ctx.Request(), ctx.Response()
	if request.Header.Get("Origin") != routes.issuer ||
		request.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return routes.oauthError(
			ctx,
			http.StatusUnauthorized,
			"invalid_request",
			"logout request was rejected",
		)
	}
	cookieName, secure := routes.browserSessionCookie()
	cookie, err := request.Cookie(cookieName)
	if err == nil {
		if err := routes.fosite.RevokeBrowserSession(request.Context(), cookie.Value); err != nil {
			return routes.oauthError(
				ctx,
				http.StatusServiceUnavailable,
				"temporarily_unavailable",
				"logout could not be completed",
			)
		}
	}
	//nolint:gosec // Secure follows the validated issuer URL; this only expires the existing browser cookie.
	http.SetCookie(
		writer,
		&http.Cookie{Name: cookieName, Value: "", Path: "/", Secure: secure,
			HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(1, 0).UTC()},
	)
	return ctx.NoContent(http.StatusNoContent)
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
		ctx.Response().
			Header().
			Set("WWW-Authenticate", `Bearer error="invalid_token"`)
		return routes.oauthError(
			ctx,
			http.StatusUnauthorized,
			"invalid_token",
			"access token was rejected",
		)
	}
	return ctx.JSON(
		http.StatusOK,
		map[string]any{
			"sub":   session.IdentityID,
			"name":  session.DisplayName,
			"email": session.Email,
		},
	)
}
