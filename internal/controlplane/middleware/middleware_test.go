package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/labstack/echo/v5"
)

type recordingAuthorizer struct{ calls int }

func (authorizer *recordingAuthorizer) Authorize(context.Context, authorization.Subject, authorization.Request) authorization.Decision {
	authorizer.calls++
	return authorization.Decision{}
}

func TestErrorStatusMapping(t *testing.T) {
	tests := map[controlplaneapi.ErrorCode]int{
		controlplaneapi.CodeUnauthenticated: http.StatusUnauthorized,
		controlplaneapi.CodeForbidden:       http.StatusForbidden,
		controlplaneapi.CodeNotFound:        http.StatusNotFound,
		controlplaneapi.CodeConflict:        http.StatusConflict,
		controlplaneapi.CodeInvalidArgument: http.StatusBadRequest,
		controlplaneapi.CodeUnavailable:     http.StatusServiceUnavailable,
		controlplaneapi.CodeVersionMismatch: http.StatusUpgradeRequired,
		controlplaneapi.CodeRateLimited:     http.StatusTooManyRequests,
		controlplaneapi.CodeInternal:        http.StatusInternalServerError,
	}
	for code, expectedStatus := range tests {
		response := httptest.NewRecorder()
		ctx := echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/", nil), response)
		writeError(ctx, "request-id", &controlplaneapi.Error{Code: code})
		if response.Code != expectedStatus {
			t.Errorf("%s status = %d, want %d", code, response.Code, expectedStatus)
		}
	}
}

func TestWebSocketUpgradeDetectionIsStrict(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/sessions/id/exec/id/stream", nil)
	request.Header.Set("Connection", "keep-alive, Upgrade")
	request.Header.Set("Upgrade", "websocket")
	if !isWebSocketUpgrade(request) {
		t.Fatal("valid WebSocket upgrade was not detected")
	}
	request.Method = http.MethodPost
	if isWebSocketUpgrade(request) {
		t.Fatal("non-GET request bypassed the API timeout")
	}
}

func TestAuthenticatedVersionDiscoveryRunsBeforeNamespaceAuthorization(t *testing.T) {
	authorizer := &recordingAuthorizer{}
	middleware := New(Config{
		APIPathPrefix:      "/kubeloop/api",
		RequestTimeout:     time.Second,
		MaxRequestBodySize: 1 << 20,
		Authenticator: controlplaneapi.AuthenticatorFunc(func(*http.Request) (controlplaneapi.Principal, *controlplaneapi.Error) {
			return controlplaneapi.Principal{Subject: "81a678af-e99c-4411-99dc-57fd59d83189", Provider: "local"}, nil
		}),
		Authorizer: authorizer,
	})
	request := httptest.NewRequest(http.MethodGet, "/kubeloop/api/version", nil)
	response := httptest.NewRecorder()
	ctx := echo.New().NewContext(request, response)
	err := middleware(func(ctx *echo.Context) error { return ctx.NoContent(http.StatusNoContent) })(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusNoContent {
		t.Fatalf("version status = %d", response.Code)
	}
	if authorizer.calls != 0 {
		t.Fatalf("version authorization calls = %d", authorizer.calls)
	}
}
