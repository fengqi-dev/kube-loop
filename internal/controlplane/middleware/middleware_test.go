package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/labstack/echo/v5"
)

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
