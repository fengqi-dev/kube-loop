package middleware

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v5"
	echomiddleware "github.com/labstack/echo/v5/middleware"
)

func TestEchoRequestIDAndLoggerShareCorrelationID(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	router := echo.New()
	router.Use(echomiddleware.RequestID())
	router.Use(RequestLogger(logger))
	router.GET("/items/:id", func(ctx *echo.Context) error {
		return ctx.JSON(http.StatusAccepted, map[string]string{"status": "accepted"})
	})

	request := httptest.NewRequest(http.MethodGet, "/items/42?token=secret", nil)
	request.Header.Set("X-Request-ID", "client-request-id")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Header().Get("X-Request-ID") != "client-request-id" {
		t.Fatalf("response request ID = %q", response.Header().Get("X-Request-ID"))
	}
	var event map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &event); err != nil {
		t.Fatal(err)
	}
	if event["msg"] != "http request" || event["request_id"] != "client-request-id" ||
		event["method"] != http.MethodGet || event["path"] != "/items/42" ||
		event["route"] != "/items/:id" || event["status"] != float64(http.StatusAccepted) {
		t.Fatalf("unexpected request log: %#v", event)
	}
	if bytes.Contains(logs.Bytes(), []byte("secret")) {
		t.Fatalf("request log contains query parameter: %s", logs.String())
	}
}

func TestEchoRequestIDGeneratesMissingID(t *testing.T) {
	router := echo.New()
	router.Use(echomiddleware.RequestID())
	router.GET("/", func(ctx *echo.Context) error {
		return ctx.NoContent(http.StatusNoContent)
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Header().Get("X-Request-ID") == "" {
		t.Fatal("generated request ID missing from response")
	}
}
