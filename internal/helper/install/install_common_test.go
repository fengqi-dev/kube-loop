package install

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestFileRollbackRestoresExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "helper")
	if err := os.WriteFile(path, []byte("old helper"), 0o700); err != nil {
		t.Fatal(err)
	}
	snapshot, err := snapshotFileForRollback(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("new helper"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := snapshot.restore(); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, path, "old helper")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); runtime.GOOS != "windows" && got != 0o700 {
		t.Fatalf("restored mode = %o, want 700", got)
	}
}

func TestFileRollbackRemovesNewFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "helper")
	snapshot, err := snapshotFileForRollback(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("new helper"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := snapshot.restore(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("stat restored new file error = %v, want not exist", err)
	}
}

func TestFileRollbackCommitDiscardsBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "helper")
	if err := os.WriteFile(path, []byte("old helper"), 0o700); err != nil {
		t.Fatal(err)
	}
	snapshot, err := snapshotFileForRollback(path)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.discard()
	if _, err := os.Stat(snapshot.backupPath); !os.IsNotExist(err) {
		t.Fatalf("stat discarded backup error = %v, want not exist", err)
	}
	assertFileContent(t, path, "old helper")
}

func TestFileRollbackKeepsBackupWhenRestoreFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "helper")
	if err := os.WriteFile(path, []byte("old helper"), 0o700); err != nil {
		t.Fatal(err)
	}
	snapshot, err := snapshotFileForRollback(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := snapshot.restore(); err == nil {
		t.Fatal("restore succeeded with a directory at the destination")
	}
	assertFileContent(t, snapshot.backupPath, "old helper")
}
