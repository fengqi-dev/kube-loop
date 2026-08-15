package fileopsapi

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/execapi"
)

type localPodExecutor struct{}

func (localPodExecutor) Exec(ctx context.Context, _ controlplaneapi.Identity, _ string, spec execapi.Spec, streams execapi.Streams) error {
	command := exec.CommandContext(ctx, spec.Command[0], spec.Command[1:]...)
	command.Stdin, command.Stdout, command.Stderr = streams.Stdin, streams.Stdout, streams.Stderr
	return command.Run()
}

func TestEntryParserHandlesUnusualNamesWithoutRecordConfusion(t *testing.T) {
	root := "/workspace"
	unusual := "line\ncolumn\tvalue"
	raw := []byte("41c0\t0\t1700000000\t" + root + "/directory\x00" +
		"81a0\t7\t1700000001\t" + root + "/" + unusual + "\x00")
	entries, err := parseEntries(raw, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Name != "directory" || entries[0].Kind != KindDirectory || entries[1].Name != unusual || entries[1].Size != 7 {
		t.Fatalf("entries = %#v", entries)
	}
}

func TestKubernetesOperatorRejectsParentAndTargetSymlinkEscape(t *testing.T) {
	base := t.TempDir()
	root, outside := filepath.Join(base, "root"), filepath.Join(base, "outside")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escaped")); err != nil {
		t.Fatal(err)
	}
	outsideFile := filepath.Join(outside, "existing")
	if err := os.WriteFile(outsideFile, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideFile, filepath.Join(root, "target-link")); err != nil {
		t.Fatal(err)
	}
	operator, err := NewKubernetesOperator(localPodExecutor{})
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{filepath.Join(root, "escaped", "created"), filepath.Join(root, "target-link")} {
		err := operator.Mutate(context.Background(), controlplaneapi.Identity{Subject: "user"}, "development", Spec{
			Action: ActionCreate, Kind: KindFile, Pod: "api-0", Container: "api", Path: target, AllowedRoot: root,
		})
		if err == nil {
			t.Fatalf("symlink target %q was accepted", target)
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "created")); !os.IsNotExist(err) {
		t.Fatalf("outside file was created: %v", err)
	}
	content, err := os.ReadFile(outsideFile)
	if err != nil || string(content) != "preserve" {
		t.Fatalf("outside target changed to %q: %v", content, err)
	}
}

func TestBoundedBufferRejectsOversizedOutput(t *testing.T) {
	buffer := &boundedBuffer{maximum: 3}
	if _, err := buffer.Write([]byte("four")); err == nil {
		t.Fatal("oversized output was accepted")
	}
}

var _ interface {
	Exec(context.Context, controlplaneapi.Identity, string, execapi.Spec, execapi.Streams) error
} = localPodExecutor{}
