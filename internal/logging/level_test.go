package logging

import (
	"log/slog"
	"testing"
)

func TestParseLevel(t *testing.T) {
	for _, test := range []struct {
		raw  string
		want slog.Level
	}{
		{raw: "debug", want: slog.LevelDebug},
		{raw: " INFO ", want: slog.LevelInfo},
		{raw: "warning", want: slog.LevelWarn},
		{raw: "warn", want: slog.LevelWarn},
		{raw: "ERROR", want: slog.LevelError},
	} {
		if got, err := ParseLevel(test.raw); err != nil || got != test.want {
			t.Fatalf("ParseLevel(%q) = %v, %v", test.raw, got, err)
		}
	}
	for _, raw := range []string{"", "trace", "-4", "verbose"} {
		if _, err := ParseLevel(raw); err == nil {
			t.Fatalf("ParseLevel(%q) unexpectedly succeeded", raw)
		}
	}
}
