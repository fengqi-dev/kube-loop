package httpauth

import (
	"errors"
	"net/http"

	httpauthservice "github.com/fengqi-dev/kube-loop/internal/controlplane/authn/httpauth/service"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn/token"
	controlplanemiddleware "github.com/fengqi-dev/kube-loop/internal/controlplane/middleware"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

const maxAuthBodyBytes = 16 << 10

type Routes struct{ service *httpauthservice.Service }

func NewRoutes(service *httpauthservice.Service) *Routes { return &Routes{service: service} }

func (routes *Routes) RegisterRoutes(group *echo.Group) {
	group.Use(routes.securityHeaders, middleware.BodyLimit(maxAuthBodyBytes))
	group.POST("/oidc/:providerID/start", routes.start)
	group.GET("/callback/:providerID", routes.callback)
	group.POST("/ad/:providerID/login", routes.password)
	group.POST("/static-token/:providerID/login", routes.staticToken)
	group.POST("/anonymous/:providerID/login", routes.anonymous)
	group.POST("/token/exchange", routes.exchange)
	group.POST("/token/refresh", routes.refresh)
	group.POST("/token/revoke", routes.revoke)
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

func (routes *Routes) staticToken(ctx *echo.Context) error {
	var requestBody strictJSON[staticTokenRequest]
	body := &requestBody.Value
	if !routes.bind(ctx, &requestBody) {
		return nil
	}
	pair, err := routes.service.StaticToken(ctx.Request().Context(), ctx.Param("providerID"), ctx.Request().RemoteAddr, body.Token, body.DeviceID)
	body.Token = ""
	return routes.writePair(ctx, pair, err)
}

func (routes *Routes) anonymous(ctx *echo.Context) error {
	var requestBody strictJSON[anonymousRequest]
	body := &requestBody.Value
	if !routes.bind(ctx, &requestBody) {
		return nil
	}
	pair, err := routes.service.Anonymous(ctx.Request().Context(), ctx.Param("providerID"), ctx.Request().RemoteAddr, body.DeviceID)
	return routes.writePair(ctx, pair, err)
}

func (routes *Routes) password(ctx *echo.Context) error {
	var requestBody strictJSON[passwordRequest]
	body := &requestBody.Value
	if !routes.bind(ctx, &requestBody) {
		return nil
	}
	pair, err := routes.service.Password(ctx.Request().Context(), ctx.Param("providerID"), ctx.Request().RemoteAddr, body.Username, body.Password, body.DeviceID)
	body.Password = ""
	return routes.writePair(ctx, pair, err)
}

func (routes *Routes) start(ctx *echo.Context) error {
	var requestBody strictJSON[startRequest]
	body := &requestBody.Value
	if !routes.bind(ctx, &requestBody) {
		return nil
	}
	result, err := routes.service.Start(ctx.Request().Context(), httpauthservice.StartRequest{
		ProviderID: ctx.Param("providerID"), ClientCallback: body.ClientCallback,
		State: body.State, Nonce: body.Nonce, PKCEChallenge: body.PKCEChallenge,
	})
	if err != nil {
		return routes.writeServiceError(ctx, err)
	}
	return ctx.JSON(http.StatusOK, startResponse{
		AuthorizationURL: result.AuthorizationURL,
		ExpiresAt:        result.ExpiresAt.Format("2006-01-02T15:04:05.000000000Z"),
	})
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

func (routes *Routes) exchange(ctx *echo.Context) error {
	var requestBody strictJSON[exchangeRequest]
	body := &requestBody.Value
	if !routes.bind(ctx, &requestBody) {
		return nil
	}
	pair, err := routes.service.Exchange(ctx.Request().Context(), body.Code, body.PKCEVerifier, body.DeviceID)
	return routes.writePair(ctx, pair, err)
}

func (routes *Routes) refresh(ctx *echo.Context) error {
	var requestBody strictJSON[refreshRequest]
	body := &requestBody.Value
	if !routes.bind(ctx, &requestBody) {
		return nil
	}
	pair, err := routes.service.Refresh(ctx.Request().Context(), body.RefreshToken)
	return routes.writePair(ctx, pair, err)
}

func (routes *Routes) revoke(ctx *echo.Context) error {
	var requestBody strictJSON[refreshRequest]
	body := &requestBody.Value
	if !routes.bind(ctx, &requestBody) {
		return nil
	}
	routes.service.Revoke(ctx.Request().Context(), body.RefreshToken)
	return ctx.NoContent(http.StatusNoContent)
}

func (routes *Routes) bind(ctx *echo.Context, destination any) bool {
	if err := ctx.Bind(destination); err != nil {
		apiError := controlplanemiddleware.BindingError(err)
		_ = ctx.JSON(http.StatusBadRequest, errorResponse{Code: "INVALID_ARGUMENT", Message: apiError.Message})
		return false
	}
	return true
}

func (routes *Routes) writePair(ctx *echo.Context, pair token.Pair, err error) error {
	if err != nil {
		return routes.writeServiceError(ctx, err)
	}
	return ctx.JSON(http.StatusOK, tokenPayload(pair))
}

func (routes *Routes) writeServiceError(ctx *echo.Context, err error) error {
	status, code, message := http.StatusServiceUnavailable, "TOKEN_UNAVAILABLE", "token service is unavailable"
	switch {
	case errors.Is(err, httpauthservice.ErrRateLimited):
		status, code, message = http.StatusTooManyRequests, "RATE_LIMITED", err.Error()
	case errors.Is(err, httpauthservice.ErrInvalidCredentials):
		status, code, message = http.StatusUnauthorized, "INVALID_CREDENTIALS", err.Error()
	case errors.Is(err, httpauthservice.ErrInvalidLoginRequest):
		status, code, message = http.StatusBadRequest, "INVALID_LOGIN_REQUEST", err.Error()
	case errors.Is(err, httpauthservice.ErrInvalidExchangeCode):
		status, code, message = http.StatusBadRequest, "INVALID_EXCHANGE_CODE", err.Error()
	case errors.Is(err, httpauthservice.ErrInvalidRefreshToken):
		status, code, message = http.StatusUnauthorized, "INVALID_REFRESH_TOKEN", err.Error()
	}
	return ctx.JSON(status, errorResponse{Code: code, Message: message})
}

func tokenPayload(pair token.Pair) tokenResponse {
	return tokenResponse{
		TokenType: pair.TokenType, AccessToken: pair.AccessToken,
		AccessExpiresAt:  pair.AccessExpiresAt.Format("2006-01-02T15:04:05.000000000Z"),
		RefreshToken:     pair.RefreshToken,
		RefreshExpiresAt: pair.RefreshExpiresAt.Format("2006-01-02T15:04:05.000000000Z"),
	}
}

func writeBrowserError(ctx *echo.Context) error {
	header := ctx.Response().Header()
	header.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	return ctx.String(http.StatusBadRequest, "KubeLoop login failed. Return to the desktop application and try again.\n")
}
