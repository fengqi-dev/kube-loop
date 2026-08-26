package middleware

import (
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v5"
	echomiddleware "github.com/labstack/echo/v5/middleware"
)

// RequestLogger returns Echo request logging middleware backed by slog.
func RequestLogger(logger *slog.Logger) echo.MiddlewareFunc {
	if logger == nil {
		logger = slog.Default()
	}
	return echomiddleware.RequestLoggerWithConfig(echomiddleware.RequestLoggerConfig{
		LogLatency:      true,
		LogProtocol:     true,
		LogRemoteIP:     true,
		LogMethod:       true,
		LogURIPath:      true,
		LogRoutePath:    true,
		LogRequestID:    true,
		LogStatus:       true,
		LogResponseSize: true,
		LogValuesFunc: func(ctx *echo.Context, values echomiddleware.RequestLoggerValues) error {
			level := slog.LevelInfo
			switch {
			case values.Status >= http.StatusInternalServerError:
				level = slog.LevelError
			case values.Status >= http.StatusBadRequest:
				level = slog.LevelWarn
			}
			attributes := []slog.Attr{
				slog.String("correlation_id", ID(ctx.Request().Context())),
				slog.String("request_id", values.RequestID),
				slog.String("method", values.Method),
				slog.String("path", values.URIPath),
				slog.String("route", values.RoutePath),
				slog.Int("status", values.Status),
				slog.Duration("duration", values.Latency),
				slog.String("remote_ip", values.RemoteIP),
				slog.String("protocol", values.Protocol),
				slog.Int64("response_size", values.ResponseSize),
			}
			if values.Error != nil {
				attributes = append(attributes, slog.Any("error", values.Error))
			}
			logger.LogAttrs(ctx.Request().Context(), level, "http request", attributes...)
			return nil
		},
	})
}
