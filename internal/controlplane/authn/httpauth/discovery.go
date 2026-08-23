package httpauth

import (
	"net/http"

	"github.com/labstack/echo/v5"
)

func (routes *Routes) discovery(ctx *echo.Context) error {
	issuer := routes.issuer
	return ctx.JSON(http.StatusOK, discoveryResponse{
		Issuer: issuer, AuthorizationEndpoint: issuer + oauthPath + "/authorize",
		TokenEndpoint: issuer + oauthPath + "/token", UserInfoEndpoint: issuer + oauthPath + "/userinfo",
		JWKSURI: issuer + oauthPath + "/jwks", RevocationEndpoint: issuer + oauthPath + "/revoke",
		ScopesSupported: []string{
			"openid",
			"profile",
			"email",
			"offline_access",
			"kubeloop.api",
		},
		ResponseTypesSupported: []string{"code"},
		GrantTypesSupported: []string{
			"authorization_code",
			"refresh_token",
			"client_credentials",
		},
		SubjectTypesSupported:             []string{"public"},
		IDTokenSigningAlgorithmsSupported: []string{"ES256"},
		CodeChallengeMethodsSupported:     []string{"S256"},
		TokenEndpointAuthMethodsSupported: []string{
			"none",
			"client_secret_basic",
			"client_secret_post",
		},
	})
}
