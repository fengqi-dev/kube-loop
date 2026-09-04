package filetransfer

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
)

func TestManagerUploadsDirectorySnapshotAndCleansTemporaryArchive(t *testing.T) {
	received := make(chan []byte, 1)
	server := uploadServer(t, func(value []byte) { received <- value })
	defer server.Close()
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.MkdirAll(filepath.Join(source, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "nested", "report.txt"), []byte("report"), 0o600); err != nil {
		t.Fatal(err)
	}
	temporaryDir := filepath.Join(root, "temporary")
	events := make(chan Task, 32)
	manager, err := NewManager(testClient{endpoint: websocketURL(server.URL)}, Config{
		TemporaryDir: temporaryDir,
		MaximumBytes: 1 << 20,
		OnEvent:      func(task Task) { events <- task },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer checkTestClose(t, manager.Shutdown)
	task, err := manager.Start(profile.Profile{ID: "server"}, activeFileSession(), Request{
		ProfileID: "server", Direction: fileTransferDirectionUpload, Kind: fileTransferKindDirectory,
		Pod: "api-0", LocalPath: source, RemotePath: "/workspace/source",
	})
	if err != nil {
		t.Fatal(err)
	}
	completed := waitTransferTask(t, events, task.ID)
	if completed.Status != StatusCompleted || completed.TotalBytes == 0 || completed.Checksum == "" {
		t.Fatalf("completed task = %#v", completed)
	}
	select {
	case archive := <-received:
		reader := tar.NewReader(bytes.NewReader(archive))
		found := false
		for {
			header, err := reader.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			contents, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			if header.Name == "nested/report.txt" && string(contents) == "report" {
				found = true
			}
		}
		if !found {
			t.Fatal("uploaded directory archive does not contain nested/report.txt")
		}
	case <-time.After(time.Second):
		t.Fatal("server did not receive directory upload")
	}
	temporary, err := filepath.Glob(filepath.Join(temporaryDir, "kubeloop-upload-*.tar"))
	if err != nil || len(temporary) != 0 {
		t.Fatalf("temporary upload archives = %#v err = %v", temporary, err)
	}
}

func TestManagerDownloadsThroughSameDirectoryTemporaryAndPublishes(t *testing.T) {
	contents := bytes.Repeat([]byte("manager-download-"), 20_000)
	server := downloadServer(t, contents)
	defer server.Close()
	root := t.TempDir()
	destination := filepath.Join(root, "destination.bin")
	events := make(chan Task, 32)
	manager, err := NewManager(testClient{endpoint: websocketURL(server.URL)}, Config{
		MaximumBytes: 1 << 20, OnEvent: func(task Task) { events <- task },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer checkTestClose(t, manager.Shutdown)
	task, err := manager.Start(profile.Profile{ID: "server"}, activeFileSession(), Request{
		ProfileID: "server", Direction: fileTransferDirectionDownload, Kind: fileTransferKindFile, Pod: "api-0",
		LocalPath: destination, RemotePath: "/workspace/destination.bin",
	})
	if err != nil {
		t.Fatal(err)
	}
	completed := waitTransferTask(t, events, task.ID)
	if completed.Status != StatusCompleted {
		t.Fatalf("completed task = %#v", completed)
	}
	downloaded, err := os.ReadFile(destination)
	if err != nil || !bytes.Equal(downloaded, contents) {
		t.Fatalf("downloaded bytes = %d err = %v", len(downloaded), err)
	}
	temporary, _ := filepath.Glob(filepath.Join(root, ".kubeloop-*.part"))
	if len(temporary) != 0 {
		t.Fatalf("temporary downloads remain: %#v", temporary)
	}
}

func TestManagerRejectsUnsafeDownloadedDirectoryWithoutPublishing(t *testing.T) {
	archive := tarBytes(
		t,
		tar.Header{Name: "safe/../escape", Typeflag: tar.TypeReg, Mode: 0o600, Size: 4},
		[]byte("evil"),
	)
	server := downloadServer(t, archive)
	defer server.Close()
	root := t.TempDir()
	destination := filepath.Join(root, fileTransferKindDirectory)
	events := make(chan Task, 32)
	manager, err := NewManager(testClient{endpoint: websocketURL(server.URL)}, Config{
		MaximumBytes: 1 << 20, OnEvent: func(task Task) { events <- task },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer checkTestClose(t, manager.Shutdown)
	task, err := manager.Start(profile.Profile{ID: "server"}, activeFileSession(), Request{
		ProfileID: "server", Direction: fileTransferDirectionDownload, Kind: fileTransferKindDirectory, Pod: "api-0",
		LocalPath: destination, RemotePath: "/workspace/directory",
	})
	if err != nil {
		t.Fatal(err)
	}
	failed := waitTransferTask(t, events, task.ID)
	if failed.Status != StatusFailed || failed.Error == "" {
		t.Fatalf("failed task = %#v", failed)
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Fatalf("unsafe destination was published: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "escape")); !os.IsNotExist(err) {
		t.Fatalf("archive escaped extraction root: %v", err)
	}
}
