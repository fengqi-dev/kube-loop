package httpmiddleware

import (
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
	echomiddleware "github.com/labstack/echo/v5/middleware"
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
			return wrapped(ctx)
		}
	}
}

func canonicalUUID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value
}
