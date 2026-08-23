package sshserver

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/pkg/sftp"
)

type cancelAwareExecutor struct{}

func (cancelAwareExecutor) Exec(ctx context.Context, _ Target, _ []string, _ Streams) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestSFTPHandlerListsAndStatsRemotePaths(t *testing.T) {
	executor := &fakeExecutor{files: map[string][]byte{"/tmp/hello.txt": []byte("hello")}}
	handler := newSFTPHandler(t.Context(), executor, Target{
		Context: "dev", Namespace: "default", Pod: "api", Container: "api",
	})

	listed, err := handler.Filelist(sftp.NewRequest("List", "/tmp"))
	if err != nil {
		t.Fatal(err)
	}
	items := make([]os.FileInfo, 2)
	count, listErr := listed.ListAt(items, 0)
	if count != 1 || (listErr != nil && !errors.Is(listErr, io.EOF)) || items[0].Name() != "hello.txt" {
		t.Fatalf("count=%d err=%v items=%#v", count, listErr, items[:count])
	}

	for _, method := range []string{"Stat", "Lstat", "Readlink"} {
		t.Run(method, func(t *testing.T) {
			result, err := handler.Filelist(sftp.NewRequest(method, "/tmp/hello.txt"))
			if err != nil {
				t.Fatal(err)
			}
			items := make([]os.FileInfo, 1)
			if count, err := result.ListAt(items, 0); count != 1 || (err != nil && !errors.Is(err, io.EOF)) {
				t.Fatalf("count=%d err=%v", count, err)
			}
		})
	}

	lstat, err := handler.Lstat(sftp.NewRequest("Lstat", "/tmp/hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if count, err := lstat.ListAt(make([]os.FileInfo, 1), 0); count != 1 ||
		(err != nil && !errors.Is(err, io.EOF)) {
		t.Fatalf("Lstat count=%d err=%v", count, err)
	}
	if _, err := handler.Filelist(sftp.NewRequest("Unknown", "/tmp")); err == nil {
		t.Fatal("unsupported list operation was accepted")
	}
}

func TestSFTPHandlerPathAndCommandContracts(t *testing.T) {
	executor := &fakeExecutor{}
	handler := newSFTPHandler(t.Context(), executor, Target{Context: "dev", Namespace: "default", Pod: "api"})

	if got, err := handler.RealPath("../tmp/./hello.txt"); err != nil || got != "/tmp/hello.txt" {
		t.Fatalf("RealPath() = %q, %v", got, err)
	}
	if got, err := handler.Readlink("/tmp/link"); err != nil || got != "exec-ok" {
		t.Fatalf("Readlink() = %q, %v", got, err)
	}

	tests := []struct {
		name   string
		method string
		path   string
		target string
		want   string
	}{
		{name: "mkdir", method: "Mkdir", path: "/tmp/new", want: "mkdir -- /tmp/new"},
		{name: "rmdir", method: "Rmdir", path: "/tmp/old", want: "rmdir -- /tmp/old"},
		{name: "remove", method: "Remove", path: "/tmp/old", want: "rm -f -- /tmp/old"},
		{
			name: "rename", method: "Rename", path: "/tmp/old", target: "/tmp/new",
			want: "mv -- /tmp/old /tmp/new",
		},
		{
			name: "symlink", method: "Symlink", path: "/tmp/source", target: "/tmp/link",
			want: "ln -s -- /tmp/source /tmp/link",
		},
		{
			name: "link", method: "Link", path: "/tmp/source", target: "/tmp/link",
			want: "ln -- /tmp/source /tmp/link",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := sftp.NewRequest(test.method, test.path)
			request.Target = test.target
			if err := handler.Filecmd(request); err != nil {
				t.Fatal(err)
			}
			executor.mu.Lock()
			command := executor.commands[len(executor.commands)-1]
			executor.mu.Unlock()
			if len(command) < 3 || command[2] != test.want {
				t.Fatalf("command=%#v, want script %q", command, test.want)
			}
		})
	}

	rename := sftp.NewRequest("PosixRename", "/tmp/old")
	rename.Target = "/tmp/new"
	if err := handler.PosixRename(rename); err != nil {
		t.Fatal(err)
	}
	if err := handler.Filecmd(sftp.NewRequest("Unknown", "/tmp/value")); err == nil {
		t.Fatal("unsupported command was accepted")
	}
}

func TestSFTPHandlerReadlinkUsesSessionContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	handler := newSFTPHandler(ctx, cancelAwareExecutor{}, Target{
		Context: "dev", Namespace: "default", Pod: "api",
	})
	result := make(chan error, 1)
	go func() {
		_, err := handler.Readlink("/tmp/link")
		result <- err
	}()
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Readlink error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Readlink ignored session cancellation")
	}
}

func TestCleanRemotePathContracts(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "empty", want: "/"},
		{name: "relative", raw: "tmp/file", want: "/tmp/file"},
		{name: "parent", raw: "/tmp/../etc/config", want: "/etc/config"},
		{name: "duplicate separators", raw: "//tmp///file", want: "/tmp/file"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := cleanRemotePath(test.raw); got != test.want {
				t.Fatalf("cleanRemotePath(%q) = %q, want %q", test.raw, got, test.want)
			}
		})
	}
}

func TestSFTPHandlerSetstatBuildsExplicitCommands(t *testing.T) {
	executor := &fakeExecutor{}
	handler := newSFTPHandler(t.Context(), executor, Target{Context: "dev", Namespace: "default", Pod: "api"})
	request := sftp.NewRequest("Setstat", "/tmp/file")
	request.Flags = 0x01 | 0x04 | 0x08 // size, permissions, access/modification time
	request.Attrs = binary.BigEndian.AppendUint64(nil, 12)
	request.Attrs = binary.BigEndian.AppendUint32(request.Attrs, 0o640)
	request.Attrs = binary.BigEndian.AppendUint32(request.Attrs, 100)
	request.Attrs = binary.BigEndian.AppendUint32(request.Attrs, 123)

	if err := handler.Filecmd(request); err != nil {
		t.Fatal(err)
	}
	executor.mu.Lock()
	command := executor.commands[len(executor.commands)-1]
	executor.mu.Unlock()
	want := "chmod 640 -- /tmp/file && truncate -s 12 -- /tmp/file && touch -m -d @123 -- /tmp/file"
	if len(command) < 3 || command[2] != want {
		t.Fatalf("command=%#v, want script %q", command, want)
	}
}
