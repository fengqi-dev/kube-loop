package httpauth

import (
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	httpauthservice "github.com/fengqi-dev/kube-loop/internal/controlplane/authn/httpauth/service"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn/token"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

var localLoginTemplate = template.Must(template.New("local-login").Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>KubeLoop 登录</title></head><body><main><h1>KubeLoop 登录</h1>
<form method="post" action="/oauth2/login/local"><input type="hidden" name="transaction" value="{{.}}">
<label>用户名 <input name="username" autocomplete="username" required></label><br>
<label>密码 <input type="password" name="password" autocomplete="current-password" required></label><br>
<label>MFA 或恢复码（已启用时填写） <input name="second_factor" autocomplete="one-time-code"></label><br>
<button type="submit">登录</button></form></main></body></html>`))

const (
	maxAuthBodyBytes        = 16 << 10
	oauthPath               = "/oauth2"
	openidConfigurationPath = "/.well-known/openid-configuration"
	oauthMetadataPath       = "/.well-known/oauth-authorization-server"
	anonymousGrantType      = "urn:kubeloop:params:oauth:grant-type:anonymous"
)

type Routes struct{ service *httpauthservice.Service }

func NewRoutes(service *httpauthservice.Service) *Routes { return &Routes{service: service} }

func (routes *Routes) RegisterRoutes(group *echo.Group) {
	group.Use(routes.securityHeaders)
	group.GET(openidConfigurationPath, routes.discovery)
	group.GET(oauthMetadataPath, routes.discovery)
	group.GET(oauthPath+"/authorize", routes.authorize)
	group.GET(oauthPath+"/callback/:providerID", routes.callback)
	group.POST(oauthPath+"/login/local", routes.localLogin, middleware.BodyLimit(maxAuthBodyBytes))
	group.POST(oauthPath+"/token", routes.token, middleware.BodyLimit(maxAuthBodyBytes))
	group.POST(oauthPath+"/revoke", routes.revoke, middleware.BodyLimit(maxAuthBodyBytes))
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
	issuer := routes.service.TokenService().Issuer()
	return ctx.JSON(http.StatusOK, discoveryResponse{
		Issuer: issuer, AuthorizationEndpoint: issuer + oauthPath + "/authorize",
		TokenEndpoint: issuer + oauthPath + "/token", UserInfoEndpoint: issuer + oauthPath + "/userinfo",
		JWKSURI: issuer + oauthPath + "/jwks", RevocationEndpoint: issuer + oauthPath + "/revoke",
		ScopesSupported:                   []string{"openid", "profile", "email", "offline_access", "kubeloop.api"},
		ResponseTypesSupported:            []string{"code"},
		GrantTypesSupported:               []string{"authorization_code", "refresh_token", anonymousGrantType},
		SubjectTypesSupported:             []string{"public"},
		IDTokenSigningAlgorithmsSupported: []string{"EdDSA"},
		CodeChallengeMethodsSupported:     []string{"S256"},
		TokenEndpointAuthMethodsSupported: []string{"none"},
	})
}

func (routes *Routes) authorize(ctx *echo.Context) error {
	query := ctx.Request().URL.Query()
	if duplicateParameter(query) || query.Get("response_type") != "code" ||
		query.Get("code_challenge_method") != "S256" {
		return routes.oauthError(ctx, http.StatusBadRequest, "invalid_request", "invalid authorization request")
	}
	request := httpauthservice.StartRequest{
		ProviderID: query.Get("provider"), ClientID: query.Get("client_id"),
		ClientCallback: query.Get("redirect_uri"), State: query.Get("state"), Nonce: query.Get("nonce"),
		PKCEChallenge: query.Get("code_challenge"), Scope: query.Get("scope"),
	}
	if request.ProviderID == "local" {
		result, err := routes.service.StartLocal(ctx.Request().Context(), request)
		if err != nil {
			return routes.oauthError(ctx, http.StatusBadRequest, "invalid_request", "authorization request was rejected")
		}
		ctx.Response().Header().Set("Content-Security-Policy", "default-src 'none'; form-action 'self'; frame-ancestors 'none'")
		ctx.Response().Header().Set("Content-Type", "text/html; charset=utf-8")
		return localLoginTemplate.Execute(ctx.Response(), result.Transaction)
	}
	result, err := routes.service.Start(ctx.Request().Context(), request)
	if err != nil {
		return routes.oauthError(ctx, http.StatusBadRequest, "invalid_request", "authorization request was rejected")
	}
	return ctx.Redirect(http.StatusFound, result.AuthorizationURL)
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
	redirectURL, err := routes.service.CompleteLocal(ctx.Request().Context(), form.Get("transaction"),
		form.Get("username"), password, form.Get("second_factor"), requestID, ctx.Request().RemoteAddr)
	if err != nil {
		return writeBrowserError(ctx)
	}
	return ctx.Redirect(http.StatusSeeOther, redirectURL)
}

func (routes *Routes) callback(ctx *echo.Context) error {
	if ctx.QueryParam("error") != "" {
		return writeBrowserError(ctx)
	}
	redirectURL, err := routes.service.Callback(
		ctx.Request().Context(), ctx.Param("providerID"), ctx.QueryParam("code"), ctx.QueryParam("state"),
	)
	if err != nil {
		return writeBrowserError(ctx)
	}
	return ctx.Redirect(http.StatusSeeOther, redirectURL)
}

func (routes *Routes) token(ctx *echo.Context) error {
	form, ok := routes.bindForm(ctx)
	if !ok {
		return nil
	}
	switch form.Get("grant_type") {
	case "authorization_code":
		pair, scope, err := routes.service.Exchange(ctx.Request().Context(), form.Get("code"),
			form.Get("code_verifier"), form.Get("client_id"), form.Get("redirect_uri"), deviceID(form))
		return routes.writePair(ctx, pair, scope, err)
	case "refresh_token":
		pair, err := routes.service.Refresh(ctx.Request().Context(), form.Get("refresh_token"))
		return routes.writePair(ctx, pair, "", err)
	case anonymousGrantType:
		pair, err := routes.service.Anonymous(ctx.Request().Context(), form.Get("provider"),
			ctx.Request().RemoteAddr, deviceID(form))
		return routes.writePair(ctx, pair, form.Get("scope"), err)
	default:
		return routes.oauthError(ctx, http.StatusBadRequest, "unsupported_grant_type", "grant type is not supported")
	}
}

func (routes *Routes) revoke(ctx *echo.Context) error {
	form, ok := routes.bindForm(ctx)
	if !ok {
		return nil
	}
	routes.service.Revoke(ctx.Request().Context(), form.Get("token"))
	return ctx.NoContent(http.StatusOK)
}

func (routes *Routes) jwks(ctx *echo.Context) error {
	ctx.Response().Header().Set("Cache-Control", "public, max-age=300")
	return ctx.JSON(http.StatusOK, routes.service.TokenService().KeySet())
}

func (routes *Routes) userInfo(ctx *echo.Context) error {
	authorization := strings.TrimSpace(ctx.Request().Header.Get("Authorization"))
	if len(authorization) < 8 || !strings.EqualFold(authorization[:7], "Bearer ") {
		ctx.Response().Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
		return routes.oauthError(ctx, http.StatusUnauthorized, "invalid_token", "access token is required")
	}
	identity, err := routes.service.Authenticate(ctx.Request().Context(), strings.TrimSpace(authorization[7:]))
	if err != nil {
		ctx.Response().Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
		return routes.oauthError(ctx, http.StatusUnauthorized, "invalid_token", "access token was rejected")
	}
	return ctx.JSON(http.StatusOK, map[string]any{
		"sub": identity.Principal.ID, "name": identity.Principal.DisplayName,
		"email": identity.Principal.Email, "groups": identity.Principal.Groups,
	})
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

func (routes *Routes) writePair(ctx *echo.Context, pair token.Pair, scope string, err error) error {
	if err != nil {
		return routes.writeServiceError(ctx, err)
	}
	expiresIn := int64(time.Until(pair.AccessExpiresAt).Seconds())
	if expiresIn < 0 {
		expiresIn = 0
	}
	return ctx.JSON(http.StatusOK, tokenResponse{TokenType: pair.TokenType, AccessToken: pair.AccessToken,
		ExpiresIn: expiresIn, RefreshToken: pair.RefreshToken,
		RefreshExpiresIn: max(int64(time.Until(pair.RefreshExpiresAt).Seconds()), 0),
		IDToken:          pair.IDToken, Scope: scope})
}

func (routes *Routes) writeServiceError(ctx *echo.Context, err error) error {
	status, code, message := http.StatusServiceUnavailable, "temporarily_unavailable", "token service is unavailable"
	switch {
	case errors.Is(err, httpauthservice.ErrRateLimited):
		status, code, message = http.StatusTooManyRequests, "temporarily_unavailable", "login rate limit exceeded"
	case errors.Is(err, httpauthservice.ErrInvalidCredentials):
		status, code, message = http.StatusBadRequest, "invalid_grant", "credentials were rejected"
	case errors.Is(err, httpauthservice.ErrInvalidLoginRequest), errors.Is(err, httpauthservice.ErrInvalidExchangeCode):
		status, code, message = http.StatusBadRequest, "invalid_grant", "authorization grant was rejected"
	case errors.Is(err, httpauthservice.ErrInvalidRefreshToken):
		status, code, message = http.StatusBadRequest, "invalid_grant", "refresh token was rejected"
	}
	return routes.oauthError(ctx, status, code, message)
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

func deviceID(form url.Values) string {
	if value := strings.TrimSpace(form.Get("device_id")); value != "" {
		return value
	}
	return strings.TrimSpace(form.Get("client_id"))
}

func writeBrowserError(ctx *echo.Context) error {
	ctx.Response().Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	return ctx.String(http.StatusBadRequest, "KubeLoop login failed. Return to the application and try again.\n")
}
