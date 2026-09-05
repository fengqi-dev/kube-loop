//go:build !windows

package forwardruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStarterReportsRedactedSingBoxStartupLog(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "sing-box")
	const ticket = "secret-ticket"
	script := "#!/bin/sh\necho 'unknown outbound type: trojan " + ticket + "' >&2\nexit 1\n"
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := (Starter{BinaryPath: binary, ReadyTimeout: 10 * time.Second}).Start(t.Context(), Options{
		SessionID: "33333333-3333-4333-8333-333333333333", Generation: 1,
		Endpoint: "wss://gateway.example.test/tunnel", RelayTicket: ticket,
	})
	if err == nil || !strings.Contains(err.Error(), "unknown outbound type: trojan [REDACTED]") {
		t.Fatalf("Start() error = %v", err)
	}
	if strings.Contains(err.Error(), ticket) {
		t.Fatalf("Start() leaked RelayTicket: %v", err)
	}
}

func TestReadLogTailReturnsBoundedSuffix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sing-box.log")
	content := strings.Repeat("x", 9<<10) + " useful error"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got := readLogTail(path, "")
	if len(got) > 8<<10 || !strings.HasSuffix(got, "useful error") {
		t.Fatalf("readLogTail() returned %d bytes", len(got))
	}
}
