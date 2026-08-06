//go:build windows

package install

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWaitElevatedResult(t *testing.T) {
	path := filepath.Join(t.TempDir(), "result.json")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		raw, _ := json.Marshal(elevatedResult{})
		_ = os.WriteFile(path, raw, 0o600)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := waitElevatedResult(ctx, path); err != nil {
		t.Fatalf("wait elevated result: %v", err)
	}
}

func TestWaitElevatedResultReturnsChildError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "result.json")
	raw, _ := json.Marshal(elevatedResult{Error: "service failed"})
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	err := waitElevatedResult(context.Background(), path)
	if err == nil || !strings.Contains(err.Error(), "service failed") {
		t.Fatalf("wait elevated result error = %v", err)
	}
}

func TestExecuteElevatedRequestRejectsUnknownOperation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "request.json")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	expected, err := fileSHA256(executable)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(elevatedRequest{ExpectedSHA256: expected})
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	err = executeElevatedRequest("unknown", path)
	if err == nil || !strings.Contains(err.Error(), "unsupported elevated operation") {
		t.Fatalf("execute elevated request error = %v", err)
	}
}

func TestLockAndVerifyElevatedSourcePreventsReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "helper.exe")
	if err := os.WriteFile(path, []byte("trusted helper"), 0o600); err != nil {
		t.Fatal(err)
	}
	expected, err := fileSHA256(path)
	if err != nil {
		t.Fatal(err)
	}
	locked, err := lockAndVerifyElevatedSource(path, expected)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0o600); err == nil {
		locked.Close()
		t.Fatal("locked elevated source was replaceable")
	}
	if err := locked.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
		t.Fatalf("replace elevated source after unlock: %v", err)
	}
}
