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
