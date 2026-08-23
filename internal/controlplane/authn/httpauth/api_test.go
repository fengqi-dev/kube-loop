package httpauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
)

func TestAuthorizationUIAllowsDesktopProtocolFormRedirect(t *testing.T) {
	t.Parallel()

	router := echo.New()
	NewRoutes(nil).RegisterRoutes(router.Group(""))
	response := httptest.NewRecorder()
	router.ServeHTTP(
		response,
		httptest.NewRequest(http.MethodGet, "/oauth2/ui/", nil),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("authorization UI status = %d", response.Code)
	}
	policy := response.Header().Get("Content-Security-Policy")
	if !strings.Contains(policy, "form-action 'self' kubeloop:") {
		t.Fatalf(
			"authorization UI CSP does not allow the desktop callback: %q",
			policy,
		)
	}
	if !strings.Contains(policy, "http://127.0.0.1:*") {
		t.Fatalf(
			"authorization UI CSP no longer allows other native loopback clients: %q",
			policy,
		)
	}
	if strings.Contains(policy, "http://*:*") ||
		strings.Contains(policy, "https://*:*") {
		t.Fatalf(
			"authorization UI CSP allows an unrestricted callback: %q",
			policy,
		)
	}
}

func TestBrowserSessionCookieSecurityMatchesIssuer(t *testing.T) {
	t.Parallel()
	tests := []struct {
		issuer string
		name   string
		secure bool
	}{
		{
			issuer: "https://kubeloop.example.test",
			name:   "__Host-kubeloop-sso",
			secure: true,
		},
		{issuer: "http://kubeloop.example.test", name: "kubeloop-sso"},
	}
	for _, test := range tests {
		routes := NewRoutes(nil, WithIssuer(test.issuer))
		name, secure := routes.browserSessionCookie()
		if name != test.name || secure != test.secure {
			t.Fatalf("issuer %q cookie = (%q, %t)", test.issuer, name, secure)
		}
	}
}

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

func TestSessionTooOld(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		maxAge   string
		authTime time.Time
		want     bool
	}{
		{name: "unspecified"},
		{name: "invalid", maxAge: "soon", want: true},
		{name: "negative", maxAge: "-1", want: true},
		{name: "missing auth time", maxAge: "60", want: true},
		{name: "current session", maxAge: "60", authTime: time.Now(), want: false},
		{name: "expired session", maxAge: "60", authTime: time.Now().Add(-2 * time.Minute), want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			ctx := echo.New().NewContext(
				httptest.NewRequest(http.MethodGet, "/", nil),
				httptest.NewRecorder(),
			)
			if !test.authTime.IsZero() {
				ctx.Set("oauth.browser.auth_time", test.authTime)
			}
			if got := sessionTooOld(ctx, test.maxAge); got != test.want {
				t.Fatalf("sessionTooOld(%q) = %t, want %t", test.maxAge, got, test.want)
			}
		})
	}
}

func TestBrowserLoginErrorURL(t *testing.T) {
	t.Parallel()

	returnTo := "?transaction=transaction-1&csrf=csrf-1&client=Management&provider=local%00Local&provider=oidc%00OIDC"
	target := browserLoginErrorURL(
		url.Values{
			queryTransaction: {"transaction-1"},
			queryCSRF:        {"csrf-1"},
			"return_to":      {returnTo},
		},
		"authentication_failed",
	)
	if !strings.HasPrefix(target, oauthPath+"/ui/?") {
		t.Fatalf("unexpected redirect target %q", target)
	}
	query, err := url.ParseQuery(strings.TrimPrefix(target, oauthPath+"/ui/?"))
	if err != nil {
		t.Fatalf("parse redirect query: %v", err)
	}
	if got := query.Get("error"); got != "authentication_failed" {
		t.Fatalf("error = %q, want authentication_failed", got)
	}
	if got := query["provider"]; len(got) != 2 {
		t.Fatalf("provider count = %d, want 2", len(got))
	}
}

func TestBrowserLoginErrorURLRejectsTamperedReturnTarget(t *testing.T) {
	t.Parallel()

	target := browserLoginErrorURL(
		url.Values{
			queryTransaction: {"transaction-1"},
			queryCSRF:        {"csrf-1"},
			"return_to":      {"?transaction=other&csrf=csrf-1"},
		},
		"authentication_failed",
	)
	if target != "" {
		t.Fatalf("target = %q, want empty", target)
	}
}

func TestBindFormRejectsMalformedRequests(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		contentType string
		body        string
		wantOK      bool
		wantValue   string
	}{
		{name: "wrong content type", contentType: "application/json", body: `{}`},
		{
			name: "duplicate parameter", contentType: "application/x-www-form-urlencoded",
			body: "decision=allow&decision=cancel",
		},
		{
			name: "valid form", contentType: "application/x-www-form-urlencoded; charset=utf-8",
			body: "decision=allow", wantOK: true, wantValue: "allow",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			request := httptest.NewRequest(http.MethodPost, oauthPath+"/login/local", strings.NewReader(test.body))
			request.Header.Set("Content-Type", test.contentType)
			response := httptest.NewRecorder()
			ctx := echo.New().NewContext(request, response)
			values, ok := NewRoutes(nil).bindForm(ctx)
			if ok != test.wantOK {
				t.Fatalf("bindForm() ok = %t, want %t", ok, test.wantOK)
			}
			if test.wantOK {
				if got := values.Get("decision"); got != test.wantValue {
					t.Fatalf("decision = %q, want %q", got, test.wantValue)
				}
				return
			}
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
			}
			var body errorResponse
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error response: %v", err)
			}
			if body.Error != "invalid_request" {
				t.Fatalf("error = %q, want invalid_request", body.Error)
			}
		})
	}
}

func TestWriteBrowserErrorReturnsGenericLockedDownResponse(t *testing.T) {
	t.Parallel()

	response := httptest.NewRecorder()
	ctx := echo.New().NewContext(
		httptest.NewRequest(http.MethodPost, oauthPath+"/login/local", nil),
		response,
	)
	if err := writeBrowserError(ctx); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusBadRequest ||
		response.Header().Get("Content-Security-Policy") != "default-src 'none'; frame-ancestors 'none'" ||
		response.Body.String() != "KubeLoop login failed. Return to the application and try again.\n" {
		t.Fatalf("browser error response = status %d headers %#v body %q",
			response.Code, response.Header(), response.Body.String())
	}
}
