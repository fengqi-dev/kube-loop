package filemanager

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/podssh"
	"github.com/kballard/go-shellquote"
)

func (m *Manager) download(ctx context.Context, task TransferTask) error {
	info, err := m.remoteStat(ctx, task.Target, task.SourcePath)
	if err != nil {
		return err
	}
	if info.Size != task.TotalBytes || !sameModTime(info.ModTime, task.SourceModTime) {
		return errSourceChanged
	}
	if err := os.MkdirAll(filepath.Dir(task.DestinationPath), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(task.TempPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	current, err := file.Stat()
	if err != nil {
		return err
	}
	offset := current.Size()
	if offset > task.TotalBytes {
		if err := file.Truncate(0); err != nil {
			return err
		}
		offset = 0
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	m.updateProgress(task.ID, offset)
	script := "cat -- " + shellquote.Join(task.SourcePath)
	if offset > 0 {
		script = "tail -c +" + strconv.FormatInt(offset+1, 10) + " -- " + shellquote.Join(task.SourcePath)
	}
	writer := &progressWriter{
		writer: file, done: offset,
		update: func(done int64) { m.updateProgress(task.ID, done) },
	}
	if err := m.exec(ctx, task.Target, script, nil, writer); err != nil {
		return err
	}
	if writer.done != task.TotalBytes {
		return fmt.Errorf("download stopped at %d of %d bytes", writer.done, task.TotalBytes)
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	err = os.Rename(task.TempPath, task.DestinationPath)
	if err != nil && task.Overwrite {
		if removeErr := os.RemoveAll(task.DestinationPath); removeErr != nil {
			return removeErr
		}
		err = os.Rename(task.TempPath, task.DestinationPath)
	}
	return err
}

func (m *Manager) upload(ctx context.Context, task TransferTask) error {
	info, err := localStat(task.SourcePath)
	if err != nil {
		return err
	}
	if info.Size != task.TotalBytes || !sameModTime(info.ModTime, task.SourceModTime) {
		return errSourceChanged
	}
	offset := int64(0)
	if remote, statErr := m.remoteStat(ctx, task.Target, task.TempPath); statErr == nil {
		offset = remote.Size
	}
	if offset > task.TotalBytes {
		_ = m.exec(ctx, task.Target, "rm -f -- "+shellquote.Join(task.TempPath), nil, io.Discard)
		offset = 0
	}
	file, err := os.Open(task.SourcePath)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	m.updateProgress(task.ID, offset)
	operator := ">"
	if offset > 0 {
		operator = ">>"
	}
	parent := path.Dir(task.TempPath)
	script := "mkdir -p -- " + shellquote.Join(parent) + " && cat " + operator + " " + shellquote.Join(task.TempPath)
	reader := &progressReader{
		reader: file, done: offset,
		update: func(done int64) { m.updateProgress(task.ID, done) },
	}
	if err := m.exec(ctx, task.Target, script, reader, io.Discard); err != nil {
		return err
	}
	if reader.done != task.TotalBytes {
		return fmt.Errorf("upload stopped at %d of %d bytes", reader.done, task.TotalBytes)
	}
	if !task.Overwrite {
		if _, statErr := m.remoteStat(ctx, task.Target, task.DestinationPath); statErr == nil {
			return fmt.Errorf("destination %s already exists", task.DestinationPath)
		}
	}
	finalize := "mv -- " + shellquote.Join(task.TempPath) + " " + shellquote.Join(task.DestinationPath)
	if task.Overwrite {
		finalize = "rm -rf -- " + shellquote.Join(task.DestinationPath) + " && " + finalize
	}
	return m.exec(ctx, task.Target, finalize, nil, io.Discard)
}

func (m *Manager) downloadDirectory(ctx context.Context, task TransferTask) error {
	info, err := m.remoteStat(ctx, task.Target, task.SourcePath)
	if err != nil {
		return err
	}
	if !info.Dir || !sameModTime(info.ModTime, task.SourceModTime) {
		return errSourceChanged
	}
	if err := os.RemoveAll(task.TempPath); err != nil {
		return err
	}
	if err := os.MkdirAll(task.TempPath, 0o755); err != nil {
		return err
	}
	m.updateProgress(task.ID, 0)

	reader, writer := io.Pipe()
	result := make(chan error, 1)
	go func() {
		script := "tar cf - -C " + shellquote.Join(task.SourcePath) + " ."
		execErr := m.exec(ctx, task.Target, script, nil, writer)
		_ = writer.CloseWithError(execErr)
		result <- execErr
	}()

	extractErr := extractArchive(ctx, reader, task.TempPath, func(done int64) {
		m.updateProgress(task.ID, done)
	})
	if extractErr != nil {
		_ = reader.CloseWithError(extractErr)
	}
	_, _ = io.Copy(io.Discard, reader)
	execErr := <-result
	if extractErr != nil {
		return extractErr
	}
	if execErr != nil {
		return execErr
	}
	if !task.Overwrite {
		if _, statErr := os.Lstat(task.DestinationPath); statErr == nil {
			return fmt.Errorf("destination %s already exists", task.DestinationPath)
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
	} else if err := os.RemoveAll(task.DestinationPath); err != nil {
		return err
	}
	return os.Rename(task.TempPath, task.DestinationPath)
}

func (m *Manager) uploadDirectory(ctx context.Context, task TransferTask) error {
	info, err := localStat(task.SourcePath)
	if err != nil {
		return err
	}
	if !info.Dir || !sameModTime(info.ModTime, task.SourceModTime) {
		return errSourceChanged
	}
	total, err := localTreeSize(task.SourcePath)
	if err != nil {
		return err
	}
	if total != task.TotalBytes {
		return errSourceChanged
	}
	m.updateProgress(task.ID, 0)

	reader, writer := io.Pipe()
	result := make(chan error, 1)
	go func() {
		archiveErr := writeArchive(ctx, task.SourcePath, writer, func(done int64) {
			m.updateProgress(task.ID, done)
		})
		_ = writer.CloseWithError(archiveErr)
		result <- archiveErr
	}()
	script := "rm -rf -- " + shellquote.Join(task.TempPath) +
		" && mkdir -p -- " + shellquote.Join(task.TempPath) +
		" && tar xf - -C " + shellquote.Join(task.TempPath)
	execErr := m.exec(ctx, task.Target, script, reader, io.Discard)
	if execErr != nil {
		_ = reader.CloseWithError(execErr)
	}
	archiveErr := <-result
	if execErr != nil {
		return execErr
	}
	if archiveErr != nil {
		return archiveErr
	}
	if !task.Overwrite {
		if _, statErr := m.remoteStat(ctx, task.Target, task.DestinationPath); statErr == nil {
			return fmt.Errorf("destination %s already exists", task.DestinationPath)
		}
	}
	finalize := "mv -- " + shellquote.Join(task.TempPath) + " " + shellquote.Join(task.DestinationPath)
	if task.Overwrite {
		finalize = "rm -rf -- " + shellquote.Join(task.DestinationPath) + " && " + finalize
	}
	return m.exec(ctx, task.Target, finalize, nil, io.Discard)
}

func (m *Manager) remoteStat(ctx context.Context, target Target, remotePath string) (FileEntry, error) {
	cleaned, err := cleanRemotePath(remotePath)
	if err != nil {
		return FileEntry{}, err
	}
	parent, base := path.Split(cleaned)
	if parent == "" {
		parent = "/"
	}
	reader, writer := io.Pipe()
	result := make(chan error, 1)
	go func() {
		err := m.executor.Exec(ctx, podTarget(target), []string{
			"/bin/sh", "-c",
			"tar cf - --no-recursion -C " + shellquote.Join(parent) + " " + shellquote.Join("./"+base),
		}, podssh.Streams{Stdout: writer, Stderr: io.Discard})
		_ = writer.CloseWithError(err)
		result <- err
	}()
	archive := tar.NewReader(reader)
	header, readErr := archive.Next()
	if readErr == nil {
		_, _ = io.Copy(io.Discard, archive)
	}
	_, _ = io.Copy(io.Discard, reader)
	execErr := <-result
	if execErr != nil {
		return FileEntry{}, execErr
	}
	if readErr != nil {
		return FileEntry{}, readErr
	}
	return FileEntry{
		Name: path.Base(cleaned), Path: cleaned, Dir: header.FileInfo().IsDir(),
		Size: header.Size, Mode: uint32(header.FileInfo().Mode().Perm()),
		ModTime: header.ModTime,
	}, nil
}

func (m *Manager) exec(
	ctx context.Context,
	target Target,
	script string,
	stdin io.Reader,
	stdout io.Writer,
) error {
	var stderr bytes.Buffer
	err := m.executor.Exec(ctx, podTarget(target), []string{"/bin/sh", "-c", script}, podssh.Streams{
		Stdin: stdin, Stdout: stdout, Stderr: &stderr,
	})
	if err != nil && strings.TrimSpace(stderr.String()) != "" {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return err
}
