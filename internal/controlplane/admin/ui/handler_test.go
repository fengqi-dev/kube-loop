package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesOnlyFixedManagementAssetsWithStrictCSP(t *testing.T) {
	handler := New()
	for _, test := range []struct {
		path        string
		contentType string
		contains    string
	}{
		{path: "/ui", contentType: "text/html", contains: "KubeLoop Control"},
		{path: "/ui/callback?code=opaque", contentType: "text/html", contains: "KubeLoop Control"},
		{path: "/ui/app.css", contentType: "text/css", contains: ":root"},
		{path: "/ui/app.js", contentType: "text/javascript", contains: "managementBase"},
	} {
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK || !strings.HasPrefix(recorder.Header().Get("Content-Type"), test.contentType) ||
			!strings.Contains(recorder.Body.String(), test.contains) {
			t.Fatalf("path=%s status=%d type=%q body=%q", test.path, recorder.Code, recorder.Header().Get("Content-Type"), recorder.Body.String())
		}
		csp := recorder.Header().Get("Content-Security-Policy")
		if !strings.Contains(csp, "script-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
			t.Fatalf("path=%s CSP=%q", test.path, csp)
		}
	}

	for _, path := range []string{"/ui/unknown", "/ui/../handler.go"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("path=%s status=%d", path, recorder.Code)
		}
	}
	method := httptest.NewRecorder()
	handler.ServeHTTP(method, httptest.NewRequest(http.MethodPost, "/ui", nil))
	if method.Code != http.StatusMethodNotAllowed || method.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("method status=%d allow=%q", method.Code, method.Header().Get("Allow"))
	}
}

func TestBrowserAssetsDoNotPersistGatewayTokensOrLoadRemoteCode(t *testing.T) {
	handler := New()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/ui/app.js", nil))
	body := recorder.Body.String()
	for _, forbidden := range []string{"localStorage", "eval(", "new Function(", "https://", "http://"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("browser asset contains forbidden construct %q", forbidden)
		}
	}
	if !strings.Contains(body, `tokens.access_token = ""`) || !strings.Contains(body, `tokens.refresh_token = ""`) ||
		!strings.Contains(body, "sessionStorage.setItem(csrfStorageKey") {
		t.Fatal("browser asset does not clear transient Gateway tokens or retain the synchronizer CSRF value")
	}
	if strings.Contains(body, "hasCapability(") {
		t.Fatal("browser asset references the removed capability helper")
	}
}
