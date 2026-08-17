package httpauth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
)

func TestAuthorizationUIAllowsDesktopLoopbackFormRedirect(t *testing.T) {
	t.Parallel()

	router := echo.New()
	NewRoutes(nil).RegisterRoutes(router.Group(""))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/oauth2/ui/", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("authorization UI status = %d", response.Code)
	}
	policy := response.Header().Get("Content-Security-Policy")
	if !strings.Contains(policy, "form-action 'self' http://127.0.0.1:*") {
		t.Fatalf("authorization UI CSP does not allow the desktop callback: %q", policy)
	}
	if strings.Contains(policy, "http://*:*") || strings.Contains(policy, "https://*:*") {
		t.Fatalf("authorization UI CSP allows a non-loopback wildcard: %q", policy)
	}
}

func TestBrowserSessionCookieSecurityMatchesIssuer(t *testing.T) {
	t.Parallel()
	tests := []struct {
		issuer string
		name   string
		secure bool
	}{
		{issuer: "https://kubeloop.example.test", name: "__Host-kubeloop-sso", secure: true},
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

func TestBrowserLoginErrorURL(t *testing.T) {
	t.Parallel()

	returnTo := "?transaction=transaction-1&csrf=csrf-1&client=Management&provider=local%00Local&provider=oidc%00OIDC"
	target := browserLoginErrorURL(url.Values{
		"transaction": {"transaction-1"},
		"csrf":        {"csrf-1"},
		"return_to":   {returnTo},
	}, "authentication_failed")
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

	target := browserLoginErrorURL(url.Values{
		"transaction": {"transaction-1"},
		"csrf":        {"csrf-1"},
		"return_to":   {"?transaction=other&csrf=csrf-1"},
	}, "authentication_failed")
	if target != "" {
		t.Fatalf("target = %q, want empty", target)
	}
}
