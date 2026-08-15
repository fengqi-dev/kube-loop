package fileapi

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/execapi"
	"github.com/google/uuid"
	"github.com/kballard/go-shellquote"
)

type recordingPodExecutor struct {
	commands [][]string
	size     uint64
	checksum [32]byte
}

type localShellPodExecutor struct{}

func (localShellPodExecutor) Exec(
	ctx context.Context,
	_ controlplaneapi.Identity,
	_ string,
	spec execapi.Spec,
	streams execapi.Streams,
) error {
	command := osexec.CommandContext(ctx, spec.Command[0], spec.Command[1:]...)
	command.Stdin, command.Stdout, command.Stderr = streams.Stdin, streams.Stdout, streams.Stderr
	return command.Run()
}

func (executor *recordingPodExecutor) Exec(
	_ context.Context,
	_ controlplaneapi.Identity,
	_ string,
	spec execapi.Spec,
	streams execapi.Streams,
) error {
	executor.commands = append(executor.commands, append([]string(nil), spec.Command...))
	if streams.Stdin != nil {
		if _, err := io.Copy(io.Discard, streams.Stdin); err != nil {
			return err
		}
	}
	if len(spec.Command) == 3 && strings.Contains(spec.Command[2], "sha256sum --") {
		_, err := fmt.Fprintf(streams.Stdout, "%d\n%x  file\n", executor.size, executor.checksum)
		return err
	}
	return nil
}

func TestKubernetesTransferExecutorUsesOnlyGeneratedQuotedCommands(t *testing.T) {
	contents := []byte("payload")
	checksum := sha256.Sum256(contents)
	pods := &recordingPodExecutor{size: uint64(len(contents)), checksum: checksum}
	executor, err := NewKubernetesTransferExecutor(pods, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	remote := "/workspace/report'; touch /tmp/injected; echo '"
	taskID := "task-id"
	spec := Spec{
		Direction: DirectionUpload, Kind: KindFile, Pod: "api-0", Container: "api",
		RemotePath: remote, Size: uint64(len(contents)), Checksum: fmt.Sprintf("%x", checksum),
		AllowedRoot: "/workspace",
	}
	outcome, err := executor.Upload(context.Background(), controlplaneapi.Identity{Subject: "user"}, "development", taskID, spec, bytes.NewReader(contents))
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.HasChecksum || outcome.Checksum != checksum || outcome.Transferred != uint64(len(contents)) {
		t.Fatalf("outcome = %#v", outcome)
	}
	temporary := remote + ".kubeloop-" + taskID + ".part"
	guard := physicalPathGuard(spec.AllowedRoot, path.Dir(spec.RemotePath))
	wantScripts := []string{
		guard + "set -eu; mkdir -p -- " + shellquote.Join(path.Dir(temporary)) + "; cat > " + shellquote.Join(temporary),
		guard + "test ! -L " + shellquote.Join(temporary) + "; test -f " + shellquote.Join(temporary) + "; wc -c < " + shellquote.Join(temporary) + "; sha256sum -- " + shellquote.Join(temporary),
		guard + "set -eu; test ! -e " + shellquote.Join(remote) + "; mv -- " + shellquote.Join(temporary) + " " + shellquote.Join(remote),
	}
	if len(pods.commands) != len(wantScripts) {
		t.Fatalf("commands = %#v", pods.commands)
	}
	for index, command := range pods.commands {
		if len(command) != 3 || command[0] != "/bin/sh" || command[1] != "-c" || command[2] != wantScripts[index] {
			t.Fatalf("command[%d] = %#v, want generated script %q", index, command, wantScripts[index])
		}
	}
}

func TestArchiveValidationRejectsTraversalLinksAndOverflow(t *testing.T) {
	for _, test := range []struct {
		name     string
		header   tar.Header
		contents []byte
	}{
		{name: "parent traversal", header: tar.Header{Name: "../secret", Mode: 0o600, Size: 1, Typeflag: tar.TypeReg}, contents: []byte("x")},
		{name: "embedded parent traversal", header: tar.Header{Name: "safe/../secret", Mode: 0o600, Size: 1, Typeflag: tar.TypeReg}, contents: []byte("x")},
		{name: "absolute path", header: tar.Header{Name: "/etc/passwd", Mode: 0o600, Size: 1, Typeflag: tar.TypeReg}, contents: []byte("x")},
		{name: "symbolic link", header: tar.Header{Name: "safe/link", Linkname: "../../secret", Typeflag: tar.TypeSymlink}},
	} {
		t.Run(test.name, func(t *testing.T) {
			archive := makeArchive(t, test.header, test.contents)
			if err := validateArchive(context.Background(), bytes.NewReader(archive), io.Discard, 1<<20); err == nil {
				t.Fatal("unsafe archive was accepted")
			}
		})
	}
	total := uint64(11)
	if err := validateArchiveHeader(&tar.Header{Name: "safe", Size: 0, Typeflag: tar.TypeReg}, &total, 10); err == nil {
		t.Fatal("overflowed archive total was accepted")
	}
}

func TestKubernetesTransferExecutorRejectsParentSymlinkOutsideAllowedRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX container shell guard test")
	}
	root := t.TempDir()
	allowed := filepath.Join(root, "allowed")
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(allowed, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(allowed, "escape")); err != nil {
		t.Fatal(err)
	}
	contents := []byte("must remain inside the allowed root")
	checksum := sha256.Sum256(contents)
	executor, err := NewKubernetesTransferExecutor(localShellPodExecutor{}, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	spec := Spec{
		Direction: DirectionUpload, Kind: KindFile, Pod: "pod", Container: "container",
		RemotePath: filepath.ToSlash(filepath.Join(allowed, "escape", "data.bin")),
		Size:       uint64(len(contents)), Checksum: fmt.Sprintf("%x", checksum), AllowedRoot: filepath.ToSlash(allowed),
	}
	if _, err := executor.Upload(context.Background(), controlplaneapi.Identity{Subject: "user"}, "development", "task", spec, bytes.NewReader(contents)); err == nil {
		t.Fatal("parent symlink outside allowed root was accepted")
	}
	if _, err := os.Lstat(filepath.Join(outside, "data.bin.kubeloop-task.part")); !os.IsNotExist(err) {
		t.Fatalf("upload escaped allowed root: %v", err)
	}
}

func TestKubernetesTransferExecutorNegotiatesAndResumesStablePartialUpload(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX container shell resume test")
	}
	root := t.TempDir()
	contents := []byte("stable resumable upload")
	checksum := sha256.Sum256(contents)
	resumeID := uuid.NewString()
	remote := filepath.ToSlash(filepath.Join(root, "result.bin"))
	spec := Spec{
		Direction: DirectionUpload, Kind: KindFile, Pod: "pod", Container: "container", RemotePath: remote,
		Size: uint64(len(contents)), Checksum: fmt.Sprintf("%x", checksum), ResumeID: resumeID, AllowedRoot: filepath.ToSlash(root),
	}
	temporary := uploadTemporaryPath(spec, "ignored-task")
	if err := os.WriteFile(temporary, contents[:7], 0o600); err != nil {
		t.Fatal(err)
	}
	executor, err := NewKubernetesTransferExecutor(localShellPodExecutor{}, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	offset, err := executor.UploadOffset(context.Background(), controlplaneapi.Identity{Subject: "user"}, "development", spec)
	if err != nil || offset != 7 {
		t.Fatalf("offset = %d err = %v", offset, err)
	}
	spec.Offset = offset
	outcome, err := executor.Upload(context.Background(), controlplaneapi.Identity{Subject: "user"}, "development", "new-task", spec, bytes.NewReader(contents[offset:]))
	if err != nil || outcome.Transferred != uint64(len(contents)) || outcome.Checksum != checksum {
		t.Fatalf("outcome = %#v err = %v", outcome, err)
	}
	downloaded, err := os.ReadFile(remote)
	if err != nil || !bytes.Equal(downloaded, contents) {
		t.Fatalf("remote contents = %q err = %v", downloaded, err)
	}
}

func TestSanitizeArchiveReencodesOnlySafeMetadata(t *testing.T) {
	modTime := time.Date(2026, 8, 10, 12, 30, 15, 0, time.UTC)
	archive := makeArchive(t, tar.Header{
		Name: "safe/./file.txt", Mode: 0o10777, Size: 4, Typeflag: tar.TypeReg,
		ModTime: modTime, Uname: "root", Gname: "root", PAXRecords: map[string]string{"comment": "untrusted"},
	}, []byte("safe"))
	var sanitized bytes.Buffer
	if err := sanitizeArchive(context.Background(), bytes.NewReader(archive), &sanitized); err != nil {
		t.Fatal(err)
	}
	reader := tar.NewReader(&sanitized)
	header, err := reader.Next()
	if err != nil {
		t.Fatal(err)
	}
	contents, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if header.Name != "safe/file.txt" || header.Mode != 0o777 || header.Uname != "" || header.Gname != "" ||
		header.Linkname != "" || len(header.PAXRecords) != 0 || !header.ModTime.Equal(modTime.UTC().Truncate(time.Second)) || string(contents) != "safe" {
		t.Fatalf("sanitized header = %#v contents = %q", header, contents)
	}
	if _, err := reader.Next(); err != io.EOF {
		t.Fatalf("archive trailer error = %v", err)
	}
}

func TestCountingWriterRejectsCorruptCounterWithoutUnderflow(t *testing.T) {
	writer := &countingWriter{writer: io.Discard, maximum: 10, written: 11}
	if _, err := writer.Write([]byte("x")); err == nil {
		t.Fatal("corrupt counter was accepted")
	}
}

func makeArchive(t *testing.T, header tar.Header, contents []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	if err := writer.WriteHeader(&header); err != nil {
		t.Fatal(err)
	}
	if len(contents) > 0 {
		if _, err := writer.Write(contents); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
