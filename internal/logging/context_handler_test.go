package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/correlation"
)

func TestContextHandlerAddsCorrelationID(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	logger := slog.New(WithContext(slog.NewJSONHandler(&output, nil)))
	ctx := correlation.WithID(t.Context(), "11111111-1111-4111-8111-111111111111")
	logger.InfoContext(ctx, "connected", "session_id", "session-a")
	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	if event["correlation_id"] != "11111111-1111-4111-8111-111111111111" ||
		event["session_id"] != "session-a" {
		t.Fatalf("event = %#v", event)
	}
}
