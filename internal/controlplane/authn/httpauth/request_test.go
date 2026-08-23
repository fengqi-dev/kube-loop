package httpauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
)

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
