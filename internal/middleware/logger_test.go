package middleware

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/fengqi-dev/kube-loop/internal/utils"
)

func TestRequestLoggerUsesClientRequestID(t *testing.T) {
	const clientRequestID = "33333333-3333-4333-8333-333333333333"
	requestID := requestLog(t, clientRequestID)
	if requestID != clientRequestID {
		t.Fatalf("logged request ID = %q, want client-provided ID", requestID)
	}
}

func TestRequestLoggerReplacesNonUUIDRequestID(t *testing.T) {
	requestID := requestLog(t, "06a47d477b8014ee82a2d1eac579474f")
	if requestID == "06a47d477b8014ee82a2d1eac579474f" || !canonicalUUID(requestID) {
		t.Fatalf("non-UUID request ID was not replaced: %q", requestID)
	}
}

func TestRequestLoggerUsesGeneratedRequestID(t *testing.T) {
	requestID := requestLog(t, "")
	parsed, err := uuid.Parse(requestID)
	if err != nil {
		t.Fatalf("generated request ID %q is not a UUID: %v", requestID, err)
	}
	if parsed.String() != requestID {
		t.Fatalf("generated request ID %q is not in canonical UUID format", requestID)
	}
}

func TestRequestLoggerPropagatesCorrelationID(t *testing.T) {
	t.Parallel()
	const correlationID = "44444444-4444-4444-8444-444444444444"
	var logs bytes.Buffer
	router := echo.New()
	router.Use(RequestID())
	router.Use(RequestLogger(slog.New(slog.NewJSONHandler(&logs, nil))))
	router.GET("/items", func(ctx *echo.Context) error {
		if id := utils.CorrelationID(ctx.Request().Context()); id != correlationID {
			t.Fatalf("handler correlation ID = %q", id)
		}
		return ctx.NoContent(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "/items", nil)
	request.Header.Set(Header, correlationID)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if got := response.Header().Get(Header); got != correlationID {
		t.Fatalf("response correlation ID = %q", got)
	}
	var event map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &event); err != nil {
		t.Fatal(err)
	}
	if event["correlation_id"] != correlationID {
		t.Fatalf("logged correlation ID = %#v", event["correlation_id"])
	}
}

func TestRequestLoggerReplacesInvalidCorrelationID(t *testing.T) {
	t.Parallel()
	router := echo.New()
	router.Use(RequestID())
	router.GET("/items", func(ctx *echo.Context) error { return ctx.NoContent(http.StatusNoContent) })
	request := httptest.NewRequest(http.MethodGet, "/items", nil)
	request.Header.Set(Header, "forged\nvalue")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if got := response.Header().Get(Header); !utils.ValidCorrelationID(got) {
		t.Fatalf("response correlation ID = %q", got)
	}
}

func requestLog(t *testing.T, requestID string) string {
	t.Helper()
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	router := echo.New()
	router.Use(RequestID())
	router.Use(RequestLogger(logger))
	router.GET("/items/:id", func(ctx *echo.Context) error {
		return ctx.JSON(http.StatusAccepted, map[string]string{"status": "accepted"})
	})

	request := httptest.NewRequest(http.MethodGet, "/items/42?token=secret", nil)
	if requestID != "" {
		request.Header.Set(echo.HeaderXRequestID, requestID)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	responseRequestID := response.Header().Get(echo.HeaderXRequestID)
	if responseRequestID == "" {
		t.Fatal("response request ID is empty")
	}
	var event map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &event); err != nil {
		t.Fatal(err)
	}
	if event["msg"] != "http request" || event["method"] != http.MethodGet ||
		event["path"] != "/items/42" || event["route"] != "/items/:id" ||
		event["status"] != float64(http.StatusAccepted) {
		t.Fatalf("unexpected request log: %#v", event)
	}
	if bytes.Contains(logs.Bytes(), []byte("secret")) {
		t.Fatalf("request log contains query parameter: %s", logs.String())
	}
	loggedRequestID, _ := event["request_id"].(string)
	if loggedRequestID != responseRequestID {
		t.Fatalf("logged request ID %q differs from response ID %q", loggedRequestID, responseRequestID)
	}
	return loggedRequestID
}
