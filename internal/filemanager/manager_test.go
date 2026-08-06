package filemanager

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/podssh"
)

func TestListLocalDirectorySortsDirectoriesFirst(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "z.txt"), []byte("z"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "a-dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(nil, "")
	items, err := manager.ListLocalDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || !items[0].Dir || items[0].Name != "a-dir" ||
		items[1].Dir || items[1].Name != "z.txt" {
		t.Fatalf("unexpected entries: %+v", items)
	}
}

func TestLoadPausesInterruptedTasks(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "transfers.json")
	state := persistedState{
		Version: stateVersion,
		Tasks: []TransferTask{
			{ID: "running", Status: StatusRunning, CreatedAt: time.Now()},
			{ID: "queued", Status: StatusQueued, CreatedAt: time.Now()},
			{ID: "done", Status: StatusCompleted, CreatedAt: time.Now()},
		},
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	manager := NewManager(nil, statePath)
	items := manager.ListTransfers()
	statuses := map[string]TaskStatus{}
	for _, item := range items {
		statuses[item.ID] = item.Status
	}
	if statuses["running"] != StatusPaused || statuses["queued"] != StatusPaused {
		t.Fatalf("interrupted tasks were not paused: %+v", statuses)
	}
	if statuses["done"] != StatusCompleted {
		t.Fatalf("completed task changed state: %+v", statuses)
	}
}

func TestCleanRemotePath(t *testing.T) {
	if got, err := cleanRemotePath("/app/../data"); err != nil || got != "/data" {
		t.Fatalf("cleanRemotePath returned %q, %v", got, err)
	}
	if _, err := cleanRemotePath("relative/path"); err == nil {
		t.Fatal("expected relative path to be rejected")
	}
}

func TestLocalCreateRenameDelete(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(nil, "")

	if err := manager.CreateLocalFile(root, "created.txt"); err != nil {
		t.Fatal(err)
	}
	if err := manager.CreateLocalFile(root, "created.txt"); err == nil {
		t.Fatal("existing file was overwritten")
	}
	if err := manager.CreateLocalDirectory(root, "created"); err != nil {
		t.Fatal(err)
	}
	created := filepath.Join(root, "created")
	if err := os.WriteFile(filepath.Join(created, "file.txt"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.RenameLocalPath(created, "renamed"); err != nil {
		t.Fatal(err)
	}
	renamed := filepath.Join(root, "renamed")
	if _, err := os.Stat(filepath.Join(renamed, "file.txt")); err != nil {
		t.Fatalf("renamed directory content is unavailable: %v", err)
	}
	if err := manager.DeleteLocalPath(renamed); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(renamed); !os.IsNotExist(err) {
		t.Fatalf("deleted path still exists: %v", err)
	}
	if err := manager.CreateLocalDirectory(root, "../escape"); err == nil {
		t.Fatal("path component traversal was accepted")
	}
	if err := manager.DeleteLocalPath(""); err == nil {
		t.Fatal("empty delete path was accepted")
	}
	if err := manager.DeleteLocalPath(string(filepath.Separator)); err == nil {
		t.Fatal("filesystem root delete was accepted")
	}
}

func TestArchiveRoundTripAndTraversalProtection(t *testing.T) {
	source := t.TempDir()
	if err := os.Mkdir(filepath.Join(source, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(source, "nested", "file.txt"),
		[]byte("directory transfer"),
		0o640,
	); err != nil {
		t.Fatal(err)
	}
	var encoded bytes.Buffer
	if err := writeArchive(context.Background(), source, &encoded, func(int64) {}); err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	if err := extractArchive(
		context.Background(),
		bytes.NewReader(encoded.Bytes()),
		destination,
		func(int64) {},
	); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(destination, "nested", "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "directory transfer" {
		t.Fatalf("extracted content=%q", got)
	}

	var malicious bytes.Buffer
	archive := tar.NewWriter(&malicious)
	if err := archive.WriteHeader(&tar.Header{
		Name: "../outside.txt", Size: 1, Mode: 0o600, Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := extractArchive(
		context.Background(),
		bytes.NewReader(malicious.Bytes()),
		destination,
		func(int64) {},
	); err == nil {
		t.Fatal("archive traversal was accepted")
	}
}

func TestResumeDownloadFromPartialFile(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(root, "transfers.json")
	destination := filepath.Join(root, "data.bin")
	partial := destination + ".part"
	content := []byte("0123456789")
	if err := os.WriteFile(partial, content[:5], 0o600); err != nil {
		t.Fatal(err)
	}
	modTime := time.Unix(1_700_000_000, 0)
	state := persistedState{
		Version: stateVersion,
		Tasks: []TransferTask{{
			ID: "resume", Direction: DirectionDownload,
			Target:     Target{Context: "dev", Namespace: "default", Pod: "api", Container: "api"},
			SourcePath: "/remote/data.bin", DestinationPath: destination, TempPath: partial,
			Status: StatusPaused, TotalBytes: int64(len(content)), DoneBytes: 5,
			SourceModTime: modTime, CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}},
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	manager := NewManager(&downloadExecutor{content: content, modTime: modTime}, statePath)
	if err := manager.Resume("resume"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		items := manager.ListTransfers()
		if len(items) == 1 && items[0].Status == StatusCompleted {
			got, readErr := os.ReadFile(destination)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(got) != string(content) {
				t.Fatalf("downloaded %q; want %q", got, content)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("transfer did not complete: %+v", manager.ListTransfers())
}

func TestPodOperationsQuoteSpecialPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a local POSIX shell to emulate the Pod filesystem")
	}
	root := filepath.Join(t.TempDir(), "remote parent $")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(localShellExecutor{}, "")
	target := Target{
		Context: "dev", Namespace: "default", Pod: "api", Container: "api",
	}
	directoryName := "dir ' $;"
	fileName := "file ' $;.txt"
	renamedName := "renamed ' $;.txt"

	if err := manager.CreatePodDirectory(context.Background(), target, root, directoryName); err != nil {
		t.Fatal(err)
	}
	if err := manager.CreatePodFile(context.Background(), target, root, fileName); err != nil {
		t.Fatal(err)
	}
	if err := manager.RenamePodPath(
		context.Background(),
		target,
		filepath.Join(root, fileName),
		renamedName,
	); err != nil {
		t.Fatal(err)
	}

	items, err := manager.ListPodDirectory(context.Background(), target, root)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, item := range items {
		found[item.Name] = true
	}
	if !found[directoryName] || !found[renamedName] {
		t.Fatalf("special-path entries not listed: %+v", items)
	}

	for _, name := range []string{directoryName, renamedName} {
		if err := manager.DeletePodPath(
			context.Background(),
			target,
			filepath.Join(root, name),
		); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("deleted path %q still exists: %v", name, err)
		}
	}
}

func TestFileAndDirectoryTransfersQuoteSpecialPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a local POSIX shell to emulate the Pod filesystem")
	}
	ctx := context.Background()
	target := Target{
		Context: "dev", Namespace: "default", Pod: "api", Container: "api",
	}
	manager := NewManager(localShellExecutor{}, "")
	remoteRoot := filepath.Join(t.TempDir(), "remote $ root")
	if err := os.MkdirAll(remoteRoot, 0o700); err != nil {
		t.Fatal(err)
	}

	localSource := filepath.Join(t.TempDir(), "source ' $;.txt")
	content := []byte("quoted file transfer")
	if err := os.WriteFile(localSource, content, 0o600); err != nil {
		t.Fatal(err)
	}
	sourceInfo, err := os.Stat(localSource)
	if err != nil {
		t.Fatal(err)
	}
	remoteFile := filepath.Join(remoteRoot, "uploaded ' $;.txt")
	if err := manager.upload(ctx, TransferTask{
		ID: "upload-special", Target: target,
		SourcePath: localSource, DestinationPath: remoteFile, TempPath: remoteFile + ".part ' $;",
		TotalBytes: int64(len(content)), SourceModTime: sourceInfo.ModTime(), Overwrite: true,
	}); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(remoteFile); err != nil || !bytes.Equal(got, content) {
		t.Fatalf("uploaded content = %q, err = %v", got, err)
	}

	remoteInfo, err := manager.remoteStat(ctx, target, remoteFile)
	if err != nil {
		t.Fatal(err)
	}
	localDownload := filepath.Join(t.TempDir(), "downloaded ' $;.txt")
	if err := manager.download(ctx, TransferTask{
		ID: "download-special", Target: target,
		SourcePath: remoteFile, DestinationPath: localDownload, TempPath: localDownload + ".part",
		TotalBytes: remoteInfo.Size, SourceModTime: remoteInfo.ModTime, Overwrite: true,
	}); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(localDownload); err != nil || !bytes.Equal(got, content) {
		t.Fatalf("downloaded content = %q, err = %v", got, err)
	}

	localDirectory := filepath.Join(t.TempDir(), "source dir ' $;")
	if err := os.MkdirAll(filepath.Join(localDirectory, "nested ' $;"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(localDirectory, "nested ' $;", "data ' $;.txt"),
		[]byte("quoted directory transfer"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	directoryInfo, err := os.Stat(localDirectory)
	if err != nil {
		t.Fatal(err)
	}
	total, err := localTreeSize(localDirectory)
	if err != nil {
		t.Fatal(err)
	}
	remoteDirectory := filepath.Join(remoteRoot, "uploaded dir ' $;")
	if err := manager.uploadDirectory(ctx, TransferTask{
		ID: "upload-directory-special", Target: target, Directory: true,
		SourcePath: localDirectory, DestinationPath: remoteDirectory,
		TempPath: remoteDirectory + ".part ' $;", TotalBytes: total,
		SourceModTime: directoryInfo.ModTime(), Overwrite: true,
	}); err != nil {
		t.Fatal(err)
	}

	remoteDirectoryInfo, err := manager.remoteStat(ctx, target, remoteDirectory)
	if err != nil {
		t.Fatal(err)
	}
	localDirectoryDownload := filepath.Join(t.TempDir(), "downloaded dir ' $;")
	if err := manager.downloadDirectory(ctx, TransferTask{
		ID: "download-directory-special", Target: target, Directory: true,
		SourcePath: remoteDirectory, DestinationPath: localDirectoryDownload,
		TempPath: localDirectoryDownload + ".part", SourceModTime: remoteDirectoryInfo.ModTime,
		Overwrite: true,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(
		filepath.Join(localDirectoryDownload, "nested ' $;", "data ' $;.txt"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "quoted directory transfer" {
		t.Fatalf("downloaded directory content = %q", got)
	}
}

type localShellExecutor struct{}

func (localShellExecutor) Exec(
	ctx context.Context,
	_ podssh.Target,
	command []string,
	streams podssh.Streams,
) error {
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Stdin = streams.Stdin
	cmd.Stdout = streams.Stdout
	cmd.Stderr = streams.Stderr
	cmd.Env = append(os.Environ(), "COPYFILE_DISABLE=1")
	return cmd.Run()
}

type downloadExecutor struct {
	content []byte
	modTime time.Time
}

func (e *downloadExecutor) Exec(
	_ context.Context,
	_ podssh.Target,
	command []string,
	streams podssh.Streams,
) error {
	script := command[len(command)-1]
	if strings.HasPrefix(script, "tar cf ") {
		archive := tar.NewWriter(streams.Stdout)
		if err := archive.WriteHeader(&tar.Header{
			Name: "data.bin", Size: int64(len(e.content)),
			Mode: 0o644, ModTime: e.modTime, Typeflag: tar.TypeReg,
		}); err != nil {
			return err
		}
		if _, err := archive.Write(e.content); err != nil {
			return err
		}
		return archive.Close()
	}
	offset := 0
	if strings.HasPrefix(script, "tail -c +6 ") {
		offset = 5
	}
	_, err := io.Copy(streams.Stdout, strings.NewReader(string(e.content[offset:])))
	return err
}
