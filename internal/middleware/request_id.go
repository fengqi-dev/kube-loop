package middleware

import (
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
	echomiddleware "github.com/labstack/echo/v5/middleware"

	"github.com/fengqi-dev/kube-loop/internal/utils"
)

// RequestID returns Echo middleware that preserves a canonical UUID request ID
// and generates one when the request does not provide a valid UUID.
func RequestID() echo.MiddlewareFunc {
	requestID := echomiddleware.RequestIDWithConfig(echomiddleware.RequestIDConfig{
		Generator: uuid.NewString,
	})
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		wrapped := requestID(next)
		return func(ctx *echo.Context) error {
			values := ctx.Request().Header.Values(echo.HeaderXRequestID)
			if len(values) != 1 || !canonicalUUID(values[0]) {
				ctx.Request().Header.Del(echo.HeaderXRequestID)
			}
			correlationID := canonicalCorrelationID(
				ctx.Request().Header.Values(Header),
			)
			requestContext := utils.WithCorrelationID(ctx.Request().Context(), correlationID)
			ctx.SetRequest(ctx.Request().WithContext(requestContext))
			ctx.Request().Header.Set(Header, correlationID)
			ctx.Response().Header().Set(Header, correlationID)
			return wrapped(ctx)
		}
	}
}

func canonicalCorrelationID(values []string) string {
	if len(values) == 1 {
		value := strings.TrimSpace(values[0])
		if utils.ValidCorrelationID(value) && value == values[0] {
			return value
		}
	}
	return utils.NewCorrelationID()
}

func canonicalUUID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value
}
