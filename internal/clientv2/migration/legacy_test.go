package migration

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestPreserveLegacyStateCopiesOnceWithoutParsing(t *testing.T) {
	root := t.TempDir()
	legacy := []byte(`{"kubeconfigFiles":["/must/not/be/opened"],"clusters":{"dev":{"connected":true}}}`)
	if err := os.WriteFile(filepath.Join(root, legacyStateName), legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 10, 1, 2, 3, 4, time.UTC)
	status, err := PreserveLegacyState(root, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if !status.LegacyDetected || status.CompletedAt != now.Format(time.RFC3339Nano) {
		t.Fatalf("status = %#v", status)
	}
	backup, err := os.ReadFile(status.BackupPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != string(legacy) {
		t.Fatalf("backup changed legacy bytes: %q", backup)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(status.BackupPath)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("backup mode = %o", info.Mode().Perm())
		}
	}
	if err := os.WriteFile(filepath.Join(root, legacyStateName), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := PreserveLegacyState(root, func() time.Time { return now.Add(time.Hour) })
	if err != nil {
		t.Fatal(err)
	}
	backup, _ = os.ReadFile(second.BackupPath)
	if string(backup) != string(legacy) || second.CompletedAt != status.CompletedAt {
		t.Fatalf("second migration overwrote backup: status=%#v backup=%q", second, backup)
	}
}

func TestPreserveLegacyStateWithoutLegacyFileIsEmpty(t *testing.T) {
	status, err := PreserveLegacyState(t.TempDir(), time.Now)
	if err != nil || status.LegacyDetected {
		t.Fatalf("status = %#v err = %v", status, err)
	}
}

func TestPreserveLegacyStateRejectsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions are environment-dependent on Windows")
	}
	root := t.TempDir()
	target := filepath.Join(root, "outside.json")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, legacyStateName)); err != nil {
		t.Fatal(err)
	}
	status, err := PreserveLegacyState(root, time.Now)
	if err == nil || !status.LegacyDetected {
		t.Fatalf("status = %#v err = %v", status, err)
	}
	if _, err := os.Stat(filepath.Join(root, backupName)); !os.IsNotExist(err) {
		t.Fatalf("unexpected backup exists: %v", err)
	}
}
