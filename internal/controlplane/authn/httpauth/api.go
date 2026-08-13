package httpauth

import (
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn/oauthserver"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

const (
	maxAuthBodyBytes        = 16 << 10
	oauthPath               = "/oauth2"
	openidConfigurationPath = "/.well-known/openid-configuration"
	oauthMetadataPath       = "/.well-known/oauth-authorization-server"
	browserSessionCookie    = oauthserver.BrowserSessionCookie
	browserSessionTTL       = 12 * time.Hour
)

type RouteOption func(*Routes)

type Routes struct {
	issuer string
	fosite *oauthserver.Endpoints
}

func WithIssuer(issuer string) RouteOption {
	return func(routes *Routes) { routes.issuer = strings.TrimRight(strings.TrimSpace(issuer), "/") }
}

func NewRoutes(endpoints *oauthserver.Endpoints, options ...RouteOption) *Routes {
	routes := &Routes{fosite: endpoints}
	for _, option := range options {
		if option != nil {
			option(routes)
		}
	}
	return routes
}

func (routes *Routes) RegisterRoutes(group *echo.Group) {
	group.Use(routes.securityHeaders)
	group.GET(openidConfigurationPath, routes.discovery)
	group.GET(oauthMetadataPath, routes.discovery)
	group.GET(oauthPath+"/ui", routes.authUI)
	group.GET(oauthPath+"/ui/*", routes.authUI)
	group.GET(oauthPath+"/authorize", routes.authorize)
	group.GET(oauthPath+"/callback/:providerID", routes.callback)
	group.POST(oauthPath+"/login/provider", routes.providerLogin, middleware.BodyLimit(maxAuthBodyBytes))
	group.POST(oauthPath+"/login/local", routes.localLogin, middleware.BodyLimit(maxAuthBodyBytes))
	group.POST(oauthPath+"/token", routes.token, middleware.BodyLimit(maxAuthBodyBytes))
	group.POST(oauthPath+"/revoke", routes.revoke, middleware.BodyLimit(maxAuthBodyBytes))
	group.POST(oauthPath+"/logout", routes.logout)
	group.GET(oauthPath+"/jwks", routes.jwks)
	group.GET(oauthPath+"/userinfo", routes.userInfo)
	group.POST(oauthPath+"/userinfo", routes.userInfo, middleware.BodyLimit(maxAuthBodyBytes))
}

func (routes *Routes) securityHeaders(next echo.HandlerFunc) echo.HandlerFunc {
	return func(ctx *echo.Context) error {
		header := ctx.Response().Header()
		header.Set("Cache-Control", "no-store")
		header.Set("Pragma", "no-cache")
		header.Set("Referrer-Policy", "no-referrer")
		header.Set("X-Content-Type-Options", "nosniff")
		return next(ctx)
	}
}

func (routes *Routes) discovery(ctx *echo.Context) error {
	issuer := routes.issuer
	return ctx.JSON(http.StatusOK, discoveryResponse{
		Issuer: issuer, AuthorizationEndpoint: issuer + oauthPath + "/authorize",
		TokenEndpoint: issuer + oauthPath + "/token", UserInfoEndpoint: issuer + oauthPath + "/userinfo",
		JWKSURI: issuer + oauthPath + "/jwks", RevocationEndpoint: issuer + oauthPath + "/revoke",
		ScopesSupported:                   []string{"openid", "profile", "email", "offline_access", "kubeloop.api"},
		ResponseTypesSupported:            []string{"code", "token", "id_token", "id_token token", "code token", "code id_token", "code id_token token"},
		GrantTypesSupported:               []string{"authorization_code", "refresh_token", "password", "client_credentials", "implicit"},
		SubjectTypesSupported:             []string{"public"},
		IDTokenSigningAlgorithmsSupported: []string{"ES256"},
		CodeChallengeMethodsSupported:     []string{"S256"},
		TokenEndpointAuthMethodsSupported: []string{"none", "client_secret_basic", "client_secret_post"},
	})
}

func (routes *Routes) authorize(ctx *echo.Context) error {
	challenge, err := routes.fosite.BeginAuthorization(ctx.Request().Context(), ctx.Request())
	if err != nil {
		return routes.oauthError(ctx, http.StatusBadRequest, "invalid_request", "authorization request was rejected")
	}
	if queryProvider := ctx.QueryParam("provider"); queryProvider != "" && queryProvider != "local" {
		prompt := strings.Fields(ctx.QueryParam("prompt"))
		if slices.Contains(prompt, "none") || slices.Contains(prompt, "login") && slices.Contains(prompt, "none") {
			return routes.completeAuthorizationError(ctx, challenge, "login_required")
		}
		authorizationURL, beginErr := routes.fosite.BeginUpstreamAuthorization(ctx.Request().Context(), challenge.Transaction, challenge.CSRF, queryProvider)
		if beginErr != nil {
			return routes.completeAuthorizationError(ctx, challenge, "login_required")
		}
		return ctx.Redirect(http.StatusSeeOther, authorizationURL)
	}
	principal, hasSession := routes.existingPrincipal(ctx)
	prompt := strings.Fields(ctx.QueryParam("prompt"))
	if slices.Contains(prompt, "login") {
		hasSession = false
	}
	if hasSession && sessionTooOld(ctx, ctx.QueryParam("max_age")) {
		hasSession = false
	}
	forceConsent := slices.Contains(prompt, "consent")
	if hasSession && forceConsent && slices.Contains(prompt, "none") {
		return routes.completeAuthorizationError(ctx, challenge, "consent_required")
	}
	if hasSession && !forceConsent {
		required, consentErr := routes.fosite.ConsentRequired(ctx.Request().Context(), challenge, principal.ID)
		if consentErr == nil && !required {
			authTime, _ := ctx.Get("oauth.browser.auth_time").(time.Time)
			_ = routes.fosite.CompleteAuthorization(ctx.Response(), ctx.Request(), challenge.Transaction, challenge.CSRF, principal, false, authTime)
			return nil
		}
		if slices.Contains(prompt, "none") && consentErr == nil && required {
			return routes.completeAuthorizationError(ctx, challenge, "consent_required")
		}
	}
	if slices.Contains(prompt, "none") {
		return routes.completeAuthorizationError(ctx, challenge, "login_required")
	}
	uiQuery := url.Values{"transaction": {challenge.Transaction}, "csrf": {challenge.CSRF}, "client": {challenge.Client.Name},
		"session": {strconv.FormatBool(hasSession)}, "consent": {strconv.FormatBool(!challenge.Trusted)},
		"scope": {strings.Join(challenge.Scopes, " ")}}
	for _, descriptor := range routes.fosite.ProviderDescriptors() {
		uiQuery.Add("provider", descriptor.ID+"\x00"+descriptor.DisplayName)
	}
	return ctx.Redirect(http.StatusSeeOther, oauthPath+"/ui/?"+uiQuery.Encode())
}

func (routes *Routes) providerLogin(ctx *echo.Context) error {
	form, ok := routes.bindForm(ctx)
	if !ok {
		return nil
	}
	authorizationURL, err := routes.fosite.BeginUpstreamAuthorization(ctx.Request().Context(), form.Get("transaction"), form.Get("csrf"), form.Get("provider"))
	if err != nil {
		return writeBrowserError(ctx)
	}
	return ctx.Redirect(http.StatusSeeOther, authorizationURL)
}

func (routes *Routes) existingPrincipal(ctx *echo.Context) (storage.Principal, bool) {
	cookie, err := ctx.Request().Cookie(browserSessionCookie)
	if err == nil && strings.TrimSpace(cookie.Value) != "" {
		identity, identityErr := routes.fosite.BrowserIdentity(ctx.Request().Context(), cookie.Value)
		if identityErr == nil {
			ctx.Set("oauth.browser.auth_time", identity.AuthTime)
			return identity.Principal, true
		}
	}
	return storage.Principal{}, false
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

func (routes *Routes) completeAuthorizationError(ctx *echo.Context, challenge oauthserver.AuthorizationChallenge, code string) error {
	if code == "login_required" || code == "consent_required" {
		if err := routes.fosite.DenyAuthorization(ctx.Response(), ctx.Request(), challenge.Transaction, challenge.CSRF, code); err != nil {
			return routes.oauthError(ctx, http.StatusBadRequest, "invalid_request", "authorization request was rejected")
		}
		return nil
	}
	return routes.oauthError(ctx, http.StatusBadRequest, code, "authorization request was rejected")
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
		requestID = strings.TrimSpace(ctx.Response().Header().Get("X-Request-ID"))
	}
	if form.Get("decision") == "cancel" {
		if err := routes.fosite.CancelAuthorization(ctx.Response(), ctx.Request(), form.Get("transaction"), form.Get("csrf")); err != nil {
			return writeBrowserError(ctx)
		}
		return nil
	}
	principal, ok := routes.existingPrincipal(ctx)
	if !ok || form.Get("session") != "true" {
		var err error
		principal, err = routes.fosite.AuthenticateLocal(ctx.Request().Context(), form.Get("username"), password, form.Get("second_factor"), requestID)
		if err != nil {
			return writeBrowserError(ctx)
		}
		sessionToken, sessionErr := routes.fosite.CreateBrowserSession(ctx.Request().Context(), principal, browserSessionTTL)
		if sessionErr != nil {
			return writeBrowserError(ctx)
		}
		ctx.SetCookie(&http.Cookie{Name: browserSessionCookie, Value: sessionToken, Path: "/", Secure: true,
			HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: int(browserSessionTTL.Seconds())})
	}
	authTime, _ := ctx.Get("oauth.browser.auth_time").(time.Time)
	if err := routes.fosite.CompleteAuthorization(ctx.Response(), ctx.Request(), form.Get("transaction"), form.Get("csrf"), principal, form.Get("decision") == "allow", authTime); err != nil {
		return writeBrowserError(ctx)
	}
	return nil
}

func (routes *Routes) callback(ctx *echo.Context) error {
	if ctx.QueryParam("error") != "" {
		return writeBrowserError(ctx)
	}
	if err := routes.fosite.CompleteUpstreamAuthorization(ctx.Response(), ctx.Request(), ctx.Param("providerID"), ctx.QueryParam("code"), ctx.QueryParam("state")); err != nil {
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
		return routes.oauthError(ctx, http.StatusUnauthorized, "invalid_request", "logout request was rejected")
	}
	cookie, err := request.Cookie(browserSessionCookie)
	if err == nil {
		if err := routes.fosite.RevokeBrowserSession(request.Context(), cookie.Value); err != nil {
			return routes.oauthError(ctx, http.StatusServiceUnavailable, "temporarily_unavailable", "logout could not be completed")
		}
	}
	http.SetCookie(writer, &http.Cookie{Name: browserSessionCookie, Value: "", Path: "/", Secure: true,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(1, 0).UTC()})
	return ctx.NoContent(http.StatusNoContent)
}

func (routes *Routes) jwks(ctx *echo.Context) error {
	ctx.Response().Header().Set("Cache-Control", "public, max-age=300")
	return ctx.JSON(http.StatusOK, routes.fosite.KeySet())
}

func (routes *Routes) userInfo(ctx *echo.Context) error {
	session, _, err := routes.fosite.IntrospectAccessToken(ctx.Request().Context(), oauthserver.BearerToken(ctx.Request()))
	if err != nil || session.Machine {
		ctx.Response().Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
		return routes.oauthError(ctx, http.StatusUnauthorized, "invalid_token", "access token was rejected")
	}
	return ctx.JSON(http.StatusOK, map[string]any{"sub": session.PrincipalID, "name": session.DisplayName, "email": session.Email, "groups": session.Groups})
}

func (routes *Routes) bindForm(ctx *echo.Context) (url.Values, bool) {
	if !strings.HasPrefix(strings.ToLower(ctx.Request().Header.Get("Content-Type")), "application/x-www-form-urlencoded") {
		_ = routes.oauthError(ctx, http.StatusBadRequest, "invalid_request", "form-encoded request body is required")
		return nil, false
	}
	if err := ctx.Request().ParseForm(); err != nil || duplicateParameter(ctx.Request().PostForm) {
		_ = routes.oauthError(ctx, http.StatusBadRequest, "invalid_request", "request form is invalid")
		return nil, false
	}
	return ctx.Request().PostForm, true
}

func (routes *Routes) oauthError(ctx *echo.Context, status int, code, description string) error {
	return ctx.JSON(status, errorResponse{Error: code, ErrorDescription: description})
}

func duplicateParameter(values url.Values) bool {
	for _, items := range values {
		if len(items) != 1 {
			return true
		}
	}
	return false
}

func writeBrowserError(ctx *echo.Context) error {
	ctx.Response().Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	return ctx.String(http.StatusBadRequest, "KubeLoop login failed. Return to the application and try again.\n")
}
