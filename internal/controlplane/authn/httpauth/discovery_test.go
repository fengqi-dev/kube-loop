package httpauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v5"
)

func TestDiscoveryPublishesOAuthMetadataWithoutCaching(t *testing.T) {
	t.Parallel()

	const issuer = "https://kubeloop.example.test"
	for _, path := range []string{openidConfigurationPath, oauthMetadataPath} {
		response := httptest.NewRecorder()
		ctx := echo.New().NewContext(
			httptest.NewRequest(http.MethodGet, path, nil),
			response,
		)
		routes := NewRoutes(nil, WithIssuer(issuer+"/"))
		if err := routes.securityHeaders(routes.discovery)(ctx); err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d", path, response.Code)
		}
		var metadata discoveryResponse
		if err := json.Unmarshal(response.Body.Bytes(), &metadata); err != nil {
			t.Fatalf("decode GET %s response: %v", path, err)
		}
		if metadata.Issuer != issuer || metadata.AuthorizationEndpoint != issuer+oauthPath+"/authorize" ||
			metadata.TokenEndpoint != issuer+oauthPath+"/token" || metadata.JWKSURI != issuer+oauthPath+"/jwks" {
			t.Fatalf("GET %s metadata = %#v", path, metadata)
		}
		if response.Header().Get("Cache-Control") != "no-store" ||
			response.Header().Get("Referrer-Policy") != "no-referrer" ||
			response.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("GET %s security headers = %#v", path, response.Header())
		}
	}
}
