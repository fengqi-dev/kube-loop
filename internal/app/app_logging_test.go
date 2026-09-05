package app

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestAppLoggerWritesJSONToTerminalAndFile(t *testing.T) {
	t.Parallel()

	logPath := filepath.Join(t.TempDir(), appLogFileName)
	sink := &appLog{path: logPath}
	if err := sink.truncate(); err != nil {
		t.Fatalf("prepare application log: %v", err)
	}
	t.Cleanup(sink.close)

	var terminal bytes.Buffer
	logger := newAppLogger(sink, &terminal)
	logger.Info("session connected", "session_id", "session-1")

	assertJSONLogRecord(t, terminal.Bytes())
	fileLog, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read application log: %v", err)
	}
	assertJSONLogRecord(t, fileLog)
}

func assertJSONLogRecord(t *testing.T, line []byte) {
	t.Helper()

	var record map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(line), &record); err != nil {
		t.Fatalf("log record is not JSON: %v\n%s", err, line)
	}
	if record[slog.MessageKey] != "session connected" {
		t.Fatalf("message = %v, want session connected", record[slog.MessageKey])
	}
	if record["session_id"] != "session-1" {
		t.Fatalf("session_id = %v, want session-1", record["session_id"])
	}
}
