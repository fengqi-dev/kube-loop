package sshserver

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/kballard/go-shellquote"
	"github.com/pkg/sftp"
)

func (h *sftpHandler) stat(ctx context.Context, remotePath string) (os.FileInfo, error) {
	parent, base := splitRemotePath(remotePath)
	reader, writer := io.Pipe()
	execResult := make(chan error, 1)
	go func() {
		execResult <- h.executor.Exec(ctx, h.target, []string{
			defaultShellPath, "-c",
			"tar cf - --no-recursion -C " + shellquote.Join(parent) + " " + shellquote.Join(base),
		}, Streams{Stdout: writer, Stderr: io.Discard})
		_ = writer.Close()
	}()
	archive := tar.NewReader(reader)
	header, readErr := archive.Next()
	if readErr == nil {
		//nolint:gosec // TAR is uncompressed; draining is required to let the context-bound Pod exec command finish.
		_, _ = io.Copy(io.Discard, archive)
	}
	_, _ = io.Copy(io.Discard, reader)
	execErr := <-execResult
	if execErr != nil {
		return nil, execErr
	}
	if readErr != nil {
		if errors.Is(readErr, io.EOF) {
			return nil, os.ErrNotExist
		}
		return nil, readErr
	}
	return header.FileInfo(), nil
}

func (h *sftpHandler) list(ctx context.Context, remotePath string) ([]os.FileInfo, error) {
	item, err := h.stat(ctx, remotePath)
	if err != nil {
		return nil, err
	}
	if !item.IsDir() {
		return []os.FileInfo{item}, nil
	}
	var stdout bytes.Buffer
	if err := h.exec(ctx, "ls -A1 -- "+shellquote.Join(remotePath), &stdout); err != nil {
		return nil, err
	}
	rawNames := strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n")
	items := make([]os.FileInfo, 0, len(rawNames))
	for _, name := range rawNames {
		if name == "" {
			continue
		}
		child, statErr := h.stat(ctx, path.Join(remotePath, name))
		if statErr != nil {
			return nil, statErr
		}
		items = append(items, child)
	}
	return items, nil
}

func (h *sftpHandler) setstat(
	ctx context.Context,
	remotePath string,
	request *sftp.Request,
) error {
	attrs := request.Attributes()
	flags := request.AttrFlags()
	if attrs == nil {
		return nil
	}
	commands := make([]string, 0, 3)
	if flags.Permissions {
		commands = append(commands,
			"chmod "+strconv.FormatUint(uint64(attrs.FileMode().Perm()), 8)+" -- "+shellquote.Join(remotePath),
		)
	}
	if flags.Size {
		commands = append(commands,
			"truncate -s "+strconv.FormatUint(attrs.Size, 10)+" -- "+shellquote.Join(remotePath),
		)
	}
	if flags.Acmodtime {
		commands = append(commands,
			"touch -m -d @"+strconv.FormatUint(uint64(attrs.Mtime), 10)+" -- "+shellquote.Join(remotePath),
		)
	}
	if len(commands) == 0 {
		return nil
	}
	return h.exec(ctx, strings.Join(commands, " && "), io.Discard)
}

func (h *sftpHandler) exec(
	ctx context.Context,
	script string,
	stdout io.Writer,
) error {
	var stderr bytes.Buffer
	if stdout == nil {
		stdout = io.Discard
	}
	err := h.executor.Exec(ctx, h.target, []string{defaultShellPath, "-c", script}, Streams{
		Stdout: stdout, Stderr: &stderr,
	})
	if err != nil && strings.TrimSpace(stderr.String()) != "" {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return err
}
