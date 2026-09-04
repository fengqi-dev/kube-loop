package filetransfer

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestDirectorySnapshotAndExtractionPreserveSafeContents(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	nested := filepath.Join(source, "readonly")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	contents := []byte("directory snapshot")
	if err := os.WriteFile(filepath.Join(nested, "data.txt"), contents, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(nested, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(nested, 0o700) })
	archive, err := os.CreateTemp(root, "archive-*.tar")
	if err != nil {
		t.Fatal(err)
	}
	defer checkTestClose(t, archive.Close)
	checksum, size, err := createArchive(context.Background(), source, archive, 1<<20)
	if err != nil || size == 0 || checksum == ([32]byte{}) {
		t.Fatalf("snapshot size = %d checksum = %x err = %v", size, checksum, err)
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "destination")
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := extractArchive(context.Background(), archive, destination, 1<<20); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(destination, "readonly"), 0o700) })
	extracted, err := os.ReadFile(filepath.Join(destination, "readonly", "data.txt"))
	if err != nil || !bytes.Equal(extracted, contents) {
		t.Fatalf("extracted = %q err = %v", extracted, err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(destination, "readonly"))
		if err != nil || info.Mode().Perm() != 0o500 {
			t.Fatalf("directory mode = %v err = %v", info.Mode().Perm(), err)
		}
	}
}

func TestManagerRecoversActivePersistedTaskAsInterrupted(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(root, "transfers.json")
	now := time.Now().UTC()
	contents, err := json.Marshal(persistedState{Version: stateVersion, Tasks: []Task{
		{
			ID:         uuid.NewString(),
			ProfileID:  "server",
			SessionID:  "session",
			Namespace:  "development",
			Direction:  fileTransferDirectionUpload,
			Kind:       fileTransferKindFile,
			Pod:        "api-0",
			LocalPath:  filepath.Join(root, "source.bin"),
			RemotePath: "/workspace/source.bin",
			Status:     StatusRunning,
			CreatedAt:  now,
			UpdatedAt:  now,
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(testClient{}, Config{StatePath: statePath})
	if err != nil {
		t.Fatal(err)
	}
	defer checkTestClose(t, manager.Shutdown)
	items := manager.List("server")
	if len(items) != 1 || items[0].Status != StatusInterrupted || items[0].CompletedAt == nil || items[0].Error == "" {
		t.Fatalf("recovered tasks = %#v", items)
	}
}

func TestManagerDropsInvalidPersistedTaskAndKeepsValidHistory(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(root, "transfers.json")
	now := time.Now().UTC()
	valid := Task{
		ID:         uuid.NewString(),
		ProfileID:  "server",
		SessionID:  "session",
		Namespace:  "development",
		Direction:  fileTransferDirectionUpload,
		Kind:       fileTransferKindFile,
		Pod:        "api-0",
		LocalPath:  filepath.Join(root, "source.bin"),
		RemotePath: "/workspace/source.bin",
		Status:     StatusCompleted,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	valid.ResumeID = valid.ID
	invalid := valid
	invalid.ID = "invalid-task"
	invalid.ResumeID = invalid.ID
	contents, err := json.Marshal(persistedState{Version: stateVersion, Tasks: []Task{invalid, valid}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(testClient{}, Config{StatePath: statePath})
	if err != nil {
		t.Fatal(err)
	}
	defer checkTestClose(t, manager.Shutdown)
	items := manager.List("server")
	if len(items) != 1 || items[0].ID != valid.ID {
		t.Fatalf("recovered tasks = %#v", items)
	}
}
