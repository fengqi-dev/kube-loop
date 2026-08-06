//go:build e2e

package filemanager

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/e2e/harness"
	files "github.com/fengqi-dev/kube-loop/internal/filemanager"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestMain(m *testing.M) { harness.RunMain(m) }

func TestPodFileManagerSpecialPathsAndTransfers(t *testing.T) {
	harness.RequireE2E(t)
	ctx, cancel := harness.TestContext(t, 2*time.Minute)
	defer cancel()

	provider := harness.NewProvider(t)
	client := harness.KubeClient(t, provider)
	if err := harness.EnsureEchoWorkload(ctx, client); err != nil {
		t.Fatal(err)
	}
	podName, _ := harness.EchoPodIP(t, ctx, client)
	pod, err := client.CoreV1().Pods(harness.EchoNamespace).Get(
		ctx,
		podName,
		metav1.GetOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	target := files.Target{
		Context: harness.KubeContext(), Namespace: harness.EchoNamespace,
		Pod: podName, PodUID: string(pod.UID), Container: "sidecar",
	}
	manager := files.NewManager(provider, filepath.Join(t.TempDir(), "transfers.json"))
	t.Cleanup(manager.Shutdown)

	remoteName := fmt.Sprintf("kubeloop e2e ' $; %d", time.Now().UnixNano())
	remoteRoot := "/tmp/" + remoteName
	if err := manager.CreatePodDirectory(ctx, target, "/tmp", remoteName); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()
		_ = manager.DeletePodPath(cleanupCtx, target, remoteRoot)
	})

	if err := manager.CreatePodFile(ctx, target, remoteRoot, "created ' $;.txt"); err != nil {
		t.Fatal(err)
	}
	if err := manager.RenamePodPath(
		ctx,
		target,
		remoteRoot+"/created ' $;.txt",
		"renamed ' $;.txt",
	); err != nil {
		t.Fatal(err)
	}
	entries, err := manager.ListPodDirectory(ctx, target, remoteRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !containsEntry(entries, "renamed ' $;.txt") {
		t.Fatalf("renamed special-path file not listed: %+v", entries)
	}
	if err := manager.DeletePodPath(ctx, target, remoteRoot+"/renamed ' $;.txt"); err != nil {
		t.Fatal(err)
	}

	fileContent := []byte("KubeLoop filemanager special-path upload/download\n")
	localFileRoot := t.TempDir()
	localFile := filepath.Join(localFileRoot, "upload ' $;.txt")
	if err := os.WriteFile(localFile, fileContent, 0o600); err != nil {
		t.Fatal(err)
	}
	upload, err := manager.Start(ctx, files.TransferRequest{
		Direction: files.DirectionUpload, Target: target,
		SourcePath: localFile, DestinationDir: remoteRoot, Overwrite: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitTransfer(t, manager, upload.ID)

	localDownloadRoot := t.TempDir()
	download, err := manager.Start(ctx, files.TransferRequest{
		Direction: files.DirectionDownload, Target: target,
		SourcePath:     remoteRoot + "/upload ' $;.txt",
		DestinationDir: localDownloadRoot, Overwrite: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitTransfer(t, manager, download.ID)
	got, err := os.ReadFile(filepath.Join(localDownloadRoot, "upload ' $;.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, fileContent) {
		t.Fatalf("downloaded file = %q, want %q", got, fileContent)
	}

	localDirectory := filepath.Join(t.TempDir(), "directory ' $;")
	if err := os.MkdirAll(filepath.Join(localDirectory, "nested ' $;"), 0o755); err != nil {
		t.Fatal(err)
	}
	directoryContent := []byte("KubeLoop directory transfer\n")
	if err := os.WriteFile(
		filepath.Join(localDirectory, "nested ' $;", "data ' $;.txt"),
		directoryContent,
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	directoryUpload, err := manager.Start(ctx, files.TransferRequest{
		Direction: files.DirectionUpload, Target: target,
		SourcePath: localDirectory, DestinationDir: remoteRoot, Overwrite: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitTransfer(t, manager, directoryUpload.ID)

	localDirectoryRoot := t.TempDir()
	directoryDownload, err := manager.Start(ctx, files.TransferRequest{
		Direction: files.DirectionDownload, Target: target,
		SourcePath:     remoteRoot + "/directory ' $;",
		DestinationDir: localDirectoryRoot, Overwrite: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitTransfer(t, manager, directoryDownload.ID)
	got, err = os.ReadFile(filepath.Join(
		localDirectoryRoot,
		"directory ' $;",
		"nested ' $;",
		"data ' $;.txt",
	))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, directoryContent) {
		t.Fatalf("downloaded directory file = %q, want %q", got, directoryContent)
	}
}

func waitTransfer(t *testing.T, manager *files.Manager, id string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		for _, task := range manager.ListTransfers() {
			if task.ID != id {
				continue
			}
			switch task.Status {
			case files.StatusCompleted:
				return
			case files.StatusFailed, files.StatusStale, files.StatusCancelled:
				t.Fatalf("transfer %s ended as %s: %s", id, task.Status, task.Error)
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("transfer %s did not complete: %+v", id, manager.ListTransfers())
}

func containsEntry(entries []files.FileEntry, name string) bool {
	for _, entry := range entries {
		if entry.Name == name {
			return true
		}
	}
	return false
}
