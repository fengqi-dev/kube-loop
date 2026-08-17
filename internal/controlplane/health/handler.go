package health

import (
	"context"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"
)

type Checker interface {
	Check(context.Context) error
}

type CheckFunc func(context.Context) error

func (f CheckFunc) Check(ctx context.Context) error { return f(ctx) }

type Handler struct {
	checker Checker
	timeout time.Duration
}

func New(checker Checker, timeout time.Duration) *Handler {
	return &Handler{checker: checker, timeout: timeout}
}

func (h *Handler) Live(ctx *echo.Context) error {
	ctx.Response().Header().Set(echo.HeaderCacheControl, "no-store")
	return ctx.JSON(http.StatusOK, document{Status: "ok"})
}

func (h *Handler) Ready(ctx *echo.Context) error {
	ctx.Response().Header().Set(echo.HeaderCacheControl, "no-store")
	if h.checker == nil {
		return ctx.JSON(http.StatusOK, document{Status: "ready"})
	}
	checkContext, cancel := context.WithTimeout(ctx.Request().Context(), h.timeout)
	defer cancel()
	if err := h.checker.Check(checkContext); err != nil {
		return ctx.JSON(http.StatusServiceUnavailable, document{Status: "unavailable"})
	}
	return ctx.JSON(http.StatusOK, document{Status: "ready"})
}
