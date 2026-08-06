package session

import "testing"

func TestSingBoxLogEventAddsPrefixAndMapsLevel(t *testing.T) {
	tests := []struct {
		raw     string
		level   string
		message string
	}{
		{"INFO started", "INFO", "[sing-box] INFO started"},
		{"WARN route unavailable", "WARN", "[sing-box] WARN route unavailable"},
		{"ERROR dial failed", "ERROR", "[sing-box] ERROR dial failed"},
		{"\x1b[31mFATAL stopped\x1b[0m", "ERROR", "[sing-box] FATAL stopped"},
	}
	for _, test := range tests {
		level, message, ok := singBoxLogEvent(test.raw)
		if !ok || level != test.level || message != test.message {
			t.Fatalf("singBoxLogEvent(%q) = %q, %q, %t", test.raw, level, message, ok)
		}
	}
	if _, _, ok := singBoxLogEvent("  "); ok {
		t.Fatal("blank log line should be ignored")
	}
}
