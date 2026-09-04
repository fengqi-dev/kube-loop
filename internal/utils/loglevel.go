package utils

import (
	"errors"
	"strings"
)

// LogLevel is a log verbosity threshold. Higher Severity values are more
// important; a log record is emitted only when its own severity is at least
// the configured threshold.
type LogLevel string

const (
	LogLevelDebug LogLevel = "debug"
	LogLevelInfo  LogLevel = "info"
	LogLevelWarn  LogLevel = "warn"
	LogLevelError LogLevel = "error"
)

// Severity ranks LogLevels from least to most important.
func (level LogLevel) Severity() int {
	switch level {
	case LogLevelDebug:
		return 0
	case LogLevelInfo:
		return 1
	case LogLevelWarn:
		return 2
	case LogLevelError:
		return 3
	default:
		return 0
	}
}

// Valid reports whether level names a supported log level.
func (level LogLevel) Valid() bool {
	switch level {
	case LogLevelDebug, LogLevelInfo, LogLevelWarn, LogLevelError:
		return true
	default:
		return false
	}
}

// ParseLogLevel normalizes a raw string into a log level. It accepts both
// lower and upper case. An empty input resolves to the default (info).
func ParseLogLevel(raw string) (LogLevel, error) {
	level := LogLevel(strings.ToLower(strings.TrimSpace(raw)))
	if level == "" {
		return LogLevelInfo, nil
	}
	if !level.Valid() {
		return "", errors.New("unknown log level")
	}
	return level, nil
}

// IsEnabled reports whether a record at recordSeverity should be emitted when
// the configured threshold is threshold.
func IsEnabled(record LogLevel, threshold LogLevel) bool {
	return record.Severity() >= threshold.Severity()
}
