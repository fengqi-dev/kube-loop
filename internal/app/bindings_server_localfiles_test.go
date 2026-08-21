package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestServerLocalFilesLifecycle(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	application := &App{}

	if err := application.CreateServerLocalFile(root, "folder", serverFileKindDirectory); err != nil {
		t.Fatalf("create directory: %v", err)
	}
	if err := application.CreateServerLocalFile(root, "file.txt", serverFileKindFile); err != nil {
		t.Fatalf("create file: %v", err)
	}

	entries, err := application.ListServerLocalFiles(root)
	if err != nil {
		t.Fatalf("list directory: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entry count = %d, want 2", len(entries))
	}
	if entries[0].Name != "folder" || entries[0].Kind != serverFileKindDirectory {
		t.Fatalf("first entry = %#v, want directory first", entries[0])
	}
	if entries[1].Name != "file.txt" || entries[1].Kind != serverFileKindFile {
		t.Fatalf("second entry = %#v, want file", entries[1])
	}

	file := filepath.Join(root, "file.txt")
	if err := application.RenameServerLocalFile(file, "renamed.txt"); err != nil {
		t.Fatalf("rename file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "renamed.txt")); err != nil {
		t.Fatalf("stat renamed file: %v", err)
	}

	if err := application.DeleteServerLocalFile(filepath.Join(root, "folder")); err != nil {
		t.Fatalf("delete directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "folder")); !os.IsNotExist(err) {
		t.Fatalf("deleted directory stat error = %v, want not exist", err)
	}
}

func TestServerLocalFilesRejectInvalidTargets(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	application := &App{}
	for _, name := range []string{"", ".", "..", "nested/file"} {
		if err := application.CreateServerLocalFile(root, name, serverFileKindFile); err == nil {
			t.Errorf("CreateServerLocalFile(%q) succeeded, want error", name)
		}
	}
	if err := application.CreateServerLocalFile(root, "entry", "socket"); err == nil {
		t.Error("CreateServerLocalFile accepted unsupported kind")
	}

	volumeRoot := filepath.VolumeName(root) + string(filepath.Separator)
	if err := application.DeleteServerLocalFile(volumeRoot); err == nil {
		t.Error("DeleteServerLocalFile accepted filesystem root")
	}
}
