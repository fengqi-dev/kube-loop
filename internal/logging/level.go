package logging

import (
	"errors"
	"log/slog"
	"strings"
)

// ParseLevel accepts the stable user-facing log levels exposed by Helm and
// command-line flags. Numeric and custom slog levels are intentionally rejected.
func ParseLevel(raw string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, errors.New("log level must be debug, info, warn, or error")
	}
}
