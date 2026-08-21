package supervisorprotocol

import (
	"bytes"
	"strings"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	t.Parallel()
	request := Request{Protocol: Version, Op: OpStatus, Token: "secret"}
	var buffer bytes.Buffer
	if err := WriteFrame(&buffer, request, MaxRequestBytes); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	var got Request
	if err := ReadFrame(&buffer, &got, MaxRequestBytes); err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if got != request {
		t.Fatalf("request = %#v, want %#v", got, request)
	}
}

func TestReadFrameRejectsUnknownFields(t *testing.T) {
	t.Parallel()
	var buffer bytes.Buffer
	if err := WriteFrame(
		&buffer,
		map[string]any{"protocol": Version, "op": OpStatus, "token": "x", "path": "/tmp/x"},
		MaxRequestBytes,
	); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	var request Request
	err := ReadFrame(&buffer, &request, MaxRequestBytes)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("ReadFrame error = %v, want unknown field", err)
	}
}
