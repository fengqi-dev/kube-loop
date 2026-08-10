package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	controllerstorage "github.com/fengqi-dev/kube-loop/internal/controller/storage"
	"github.com/google/uuid"
)

func TestStorageExportAndBackupCommands(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "controller.db")
	configureSQLiteCommandTest(t, databasePath)
	store, err := controllerstorage.Open(context.Background(), controllerstorage.Config{
		Backend: controllerstorage.BackendSQLite, SQLitePath: databasePath,
	})
	if err != nil {
		t.Fatal(err)
	}
	principalID := uuid.NewString()
	if _, err := store.Principals().Upsert(context.Background(), controllerstorage.Principal{
		ID: principalID, Provider: "oidc", ExternalID: "cli-user", CreatedAt: time.Now().UTC(),
	}); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	defer store.Close()

	exportPath := filepath.Join(directory, "exports", "controller.json")
	var stdout, stderr bytes.Buffer
	if code := runStorageCommand(context.Background(), []string{"export", "--output", exportPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("export exit=%d stderr=%q", code, stderr.String())
	}
	raw, err := os.ReadFile(exportPath)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := controllerstorage.ValidateExport(raw)
	if err != nil || metadata.Rows != 1 || metadata.CreatedByVersion != controllerStorageVersion() {
		t.Fatalf("export metadata=%#v error=%v", metadata, err)
	}
	var exportResult struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &exportResult); err != nil || exportResult.Path != exportPath {
		t.Fatalf("export output=%q error=%v", stdout.String(), err)
	}
	info, err := os.Stat(exportPath)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("export mode=%o", info.Mode().Perm())
	}

	stdout.Reset()
	stderr.Reset()
	if code := runStorageCommand(context.Background(), []string{"export", "--output", exportPath}, &stdout, &stderr); code != 1 ||
		!strings.Contains(stderr.String(), "already exists") {
		t.Fatalf("duplicate export exit=%d stderr=%q", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := runStorageCommand(context.Background(), []string{"export", "--output", exportPath, "--force"}, &stdout, &stderr); code != 0 {
		t.Fatalf("forced export exit=%d stderr=%q", code, stderr.String())
	}

	backupPath := filepath.Join(directory, "backups", "controller.db")
	stdout.Reset()
	stderr.Reset()
	if code := runStorageCommand(context.Background(), []string{"backup", "--output", backupPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("backup exit=%d stderr=%q", code, stderr.String())
	}
	backup, err := controllerstorage.Open(context.Background(), controllerstorage.Config{
		Backend: controllerstorage.BackendSQLite, SQLitePath: backupPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	if _, err := backup.Principals().GetByID(context.Background(), principalID); err != nil {
		t.Fatalf("read backed up principal: %v", err)
	}
}

func TestStorageCommandRequiresExplicitImportConfirmationAndActor(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runStorageCommand(context.Background(), []string{"import", "--input", "export.json", "--actor", "operator"}, &stdout, &stderr); code != 1 ||
		!strings.Contains(stderr.String(), "--confirm-empty") {
		t.Fatalf("unconfirmed import exit=%d stderr=%q", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := runStorageCommand(context.Background(), []string{"import", "--input", "export.json", "--confirm-empty"}, &stdout, &stderr); code != 1 ||
		!strings.Contains(stderr.String(), "--actor") {
		t.Fatalf("anonymous import exit=%d stderr=%q", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := runStorageCommand(context.Background(), []string{"help"}, &stdout, &stderr); code != 0 ||
		!strings.Contains(stdout.String(), "storage export") || strings.Contains(stdout.String(), "--dsn") {
		t.Fatalf("help exit=%d stdout=%q", code, stdout.String())
	}
}

func configureSQLiteCommandTest(t *testing.T, path string) {
	t.Helper()
	t.Setenv("KUBELOOP_STORAGE_TYPE", "sqlite")
	t.Setenv("KUBELOOP_SQLITE_PATH", path)
	t.Setenv("KUBELOOP_CONTROLLER_REPLICAS", "1")
	t.Setenv("KUBELOOP_POSTGRESQL_DSN", "")
	t.Setenv("KUBELOOP_POSTGRESQL_DSN_FILE", "")
}
