package httpauth

import (
	"strings"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn/oauthserver"
)

const (
	maxAuthBodyBytes        = 16 << 10
	oauthPath               = "/oauth2"
	openidConfigurationPath = "/.well-known/openid-configuration"
	oauthMetadataPath       = "/.well-known/oauth-authorization-server"
)

type RouteOption func(*Routes)

type Routes struct {
	issuer string
	fosite *oauthserver.Endpoints
}

func WithIssuer(issuer string) RouteOption {
	return func(routes *Routes) { routes.issuer = strings.TrimRight(strings.TrimSpace(issuer), "/") }
}

func NewRoutes(
	endpoints *oauthserver.Endpoints,
	options ...RouteOption,
) *Routes {
	routes := &Routes{fosite: endpoints}
	for _, option := range options {
		if option != nil {
			option(routes)
		}
	}
	return routes
}

func (routes *Routes) browserSessionCookie() (string, bool) {
	if strings.HasPrefix(routes.issuer, "https://") {
		return oauthserver.BrowserSessionCookie, true
	}
	return oauthserver.HTTPBrowserSessionCookie, false
}

func (routes *Routes) RegisterRoutes(group *echo.Group) {
	group.Use(routes.securityHeaders)
	group.GET(openidConfigurationPath, routes.discovery)
	group.GET(oauthMetadataPath, routes.discovery)
	group.GET(oauthPath+"/ui", routes.authUI)
	group.GET(oauthPath+"/ui/*", routes.authUI)
	group.GET(oauthPath+"/authorize", routes.authorize)
	group.POST(
		oauthPath+"/login/local",
		routes.localLogin,
		middleware.BodyLimit(maxAuthBodyBytes),
	)
	group.POST(
		oauthPath+"/token",
		routes.token,
		middleware.BodyLimit(maxAuthBodyBytes),
	)
	group.POST(
		oauthPath+"/revoke",
		routes.revoke,
		middleware.BodyLimit(maxAuthBodyBytes),
	)
	group.POST(oauthPath+"/logout", routes.logout)
	group.GET(oauthPath+"/jwks", routes.jwks)
	group.GET(oauthPath+"/userinfo", routes.userInfo)
	group.POST(
		oauthPath+"/userinfo",
		routes.userInfo,
		middleware.BodyLimit(maxAuthBodyBytes),
	)
}

func (routes *Routes) securityHeaders(next echo.HandlerFunc) echo.HandlerFunc {
	return func(ctx *echo.Context) error {
		header := ctx.Response().Header()
		header.Set("Cache-Control", "no-store")
		header.Set("Pragma", "no-cache")
		header.Set("Referrer-Policy", "no-referrer")
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("Permissions-Policy", "publickey-credentials-get=()")
		return next(ctx)
	}
}
