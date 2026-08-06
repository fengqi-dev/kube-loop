package helper

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadSessionLogsIncrementally(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sing-box.log")
	if err := os.WriteFile(path, []byte("first\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := NewServer(AuthFile{})
	server.sessions["session-test"] = &session{workDir: dir}

	data, offset, err := server.readSessionLogs("session-test", 0)
	if err != nil {
		t.Fatal(err)
	}
	if data != "first\n" || offset != 6 {
		t.Fatalf("first read = %q at %d", data, offset)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("second\n"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	data, next, err := server.readSessionLogs("session-test", offset)
	if err != nil {
		t.Fatal(err)
	}
	if data != "second\n" || next != 13 {
		t.Fatalf("second read = %q at %d", data, next)
	}
}
