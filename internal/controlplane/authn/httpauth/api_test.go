package httpauth

import (
	"net/http"
	"net/http/httptest"
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

func TestLogoutRejectsCrossOriginBeforeSessionAccess(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		origin       string
		secFetchSite string
	}{
		{name: "missing origin"},
		{name: "foreign origin", origin: "https://attacker.example.test"},
		{
			name:         "cross-site fetch",
			origin:       "https://kubeloop.example.test",
			secFetchSite: "cross-site",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			request := httptest.NewRequest(http.MethodPost, "/oauth2/logout", nil)
			request.Header.Set("Origin", test.origin)
			request.Header.Set("Sec-Fetch-Site", test.secFetchSite)
			response := httptest.NewRecorder()
			ctx := echo.New().NewContext(request, response)
			routes := NewRoutes(nil, WithIssuer("https://kubeloop.example.test"))

			if err := routes.logout(ctx); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusUnauthorized ||
				!strings.Contains(response.Body.String(), `"error":"invalid_request"`) ||
				len(response.Result().Cookies()) != 0 {
				t.Fatalf(
					"logout response = status %d headers %#v body %s",
					response.Code,
					response.Header(),
					response.Body.String(),
				)
			}
		})
	}
}
