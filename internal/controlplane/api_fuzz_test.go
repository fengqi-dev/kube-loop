package controlplane

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	controlplanemiddleware "github.com/fengqi-dev/kube-loop/internal/controlplane/middleware"
)

func FuzzGatewayHTTPEntryBoundedAndRedacted(f *testing.F) {
	policy := authorization.NewAuthenticated()
	server, err := NewServer(
		Config{PublicURL: "https://gateway.example.test", MaxRequestBodyBytes: 256},
		BuildInfo{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithAuthenticator(
			controlplaneapi.AuthenticatorFunc(
				func(*http.Request) (controlplaneapi.Identity, *controlplaneapi.Error) {
					return controlplaneapi.Identity{Subject: "fuzz-identity"}, nil
				},
			),
		),
		WithAuthorizer(policy),
		WithAPIRoutes(
			testEndpoint(
				EndpointFunc(
					func(ctx *echo.Context, _ controlplaneapi.Identity) *controlplaneapi.Error {
						request := ctx.Request()
						if request.Method == http.MethodPost {
							var body struct {
								Value string `json:"value"`
							}
							if err := ctx.Bind(&body); err != nil {
								return controlplanemiddleware.BindingError(err)
							}
						}
						ctx.Response().WriteHeader(http.StatusNoContent)
						return nil
					},
				),
			),
		),
	)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(true, "resource", `{"value":"ok"}`, "application/json")
	f.Add(false, "../secret", "", "text/plain")
	f.Fuzz(func(t *testing.T, post bool, path, body, contentType string) {
		if len(path) > 512 || len(body) > 2048 || len(contentType) > 256 {
			t.Skip()
		}
		const secret = "gateway-fuzz-secret-marker"
		method := http.MethodGet
		if post {
			method = http.MethodPost
		}
		request := httptest.NewRequest(
			method,
			APIPathPrefix+"/fuzz/"+url.PathEscape(path),
			strings.NewReader(body+secret),
		)
		request.Header.Set("Content-Type", contentType)
		request.Header.Set("Authorization", "Bearer "+secret)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Body.Len() > 16<<10 {
			t.Fatalf("response exceeded bound: %d", response.Body.Len())
		}
		if strings.Contains(response.Body.String(), secret) {
			t.Fatal("request body or bearer token leaked into response")
		}
	})
}
