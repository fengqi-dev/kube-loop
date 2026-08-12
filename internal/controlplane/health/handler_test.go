package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
)

func TestLive(t *testing.T) {
	response := serve(t, New(nil, time.Second).Live)
	assertHealthResponse(t, response, http.StatusOK, "ok")
}

func TestReady(t *testing.T) {
	tests := []struct {
		name    string
		checker Checker
		status  int
		body    string
	}{
		{name: "no checker", status: http.StatusOK, body: "ready"},
		{name: "ready", checker: CheckFunc(func(context.Context) error { return nil }), status: http.StatusOK, body: "ready"},
		{name: "unavailable", checker: CheckFunc(func(context.Context) error { return errors.New("secret") }), status: http.StatusServiceUnavailable, body: "unavailable"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := serve(t, New(test.checker, time.Second).Ready)
			assertHealthResponse(t, response, test.status, test.body)
			if test.status != http.StatusOK && strings.Contains(response.Body.String(), "secret") {
				t.Fatal("readiness response leaked checker error")
			}
		})
	}
}

func TestReadyAppliesTimeout(t *testing.T) {
	handler := New(CheckFunc(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}), time.Millisecond)
	response := serve(t, handler.Ready)
	assertHealthResponse(t, response, http.StatusServiceUnavailable, "unavailable")
}

func serve(t *testing.T, handler echo.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	ctx := echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/", nil), response)
	if err := handler(ctx); err != nil {
		t.Fatal(err)
	}
	return response
}

func assertHealthResponse(t *testing.T, response *httptest.ResponseRecorder, status int, expected string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status = %d, want %d", response.Code, status)
	}
	if cacheControl := response.Header().Get(echo.HeaderCacheControl); cacheControl != "no-store" {
		t.Fatalf("Cache-Control = %q", cacheControl)
	}
	var body document
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != expected {
		t.Fatalf("status body = %q, want %q", body.Status, expected)
	}
}
