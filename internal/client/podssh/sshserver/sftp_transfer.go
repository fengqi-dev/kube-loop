package sshserver

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/kballard/go-shellquote"
)

func (h *sftpHandler) downloadFile(ctx context.Context, remotePath string) (*os.File, error) {
	file, err := os.CreateTemp("", "kubeloop-sftp-download-*")
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = file.Close()
			_ = os.Remove(file.Name())
		}
	}()

	parent, base := splitRemotePath(remotePath)
	reader, writer := io.Pipe()
	execResult := make(chan error, 1)
	go func() {
		execResult <- h.executor.Exec(ctx, h.target, []string{
			defaultShellPath, "-c",
			"tar cf - -C " + shellquote.Join(parent) + " " + shellquote.Join(base),
		}, Streams{Stdout: writer, Stderr: io.Discard})
		_ = writer.Close()
	}()

	archive := tar.NewReader(reader)
	found := false
	for {
		header, nextErr := archive.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			_ = reader.CloseWithError(nextErr)
			<-execResult
			return nil, fmt.Errorf("read Pod archive for %s: %w", remotePath, nextErr)
		}
		if found || !archiveNameMatches(header.Name, base) {
			continue
		}
		if !header.FileInfo().Mode().IsRegular() {
			_ = reader.Close()
			<-execResult
			return nil, fmt.Errorf("%s is not a regular file", remotePath)
		}
		//nolint:gosec // The authenticated Pod stream is an uncompressed TAR file; SFTP preserves its file size.
		if _, err := io.Copy(file, archive); err != nil {
			_ = reader.CloseWithError(err)
			<-execResult
			return nil, err
		}
		found = true
	}
	// tar.Reader stops after the two end-of-archive blocks, but GNU tar pads
	// streamed archives to its blocking factor (10 KiB by default). Drain that
	// padding before waiting for the Pod exec stream, otherwise its stdout
	// writer and this goroutine wait on each other indefinitely.
	_, _ = io.Copy(io.Discard, reader)
	if err := <-execResult; err != nil {
		return nil, err
	}
	if !found {
		return nil, os.ErrNotExist
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	ok = true
	return file, nil
}

func (h *sftpHandler) upload(
	ctx context.Context,
	remotePath string,
	file *os.File,
	mode os.FileMode,
) error {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	stat, err := file.Stat()
	if err != nil {
		return err
	}
	parent, base := splitRemotePath(remotePath)
	reader, writer := io.Pipe()
	writeResult := make(chan error, 1)
	go func() {
		archive := tar.NewWriter(writer)
		err := archive.WriteHeader(&tar.Header{
			Name: base, Mode: int64(mode.Perm()), Size: stat.Size(),
			ModTime: time.Now(), Typeflag: tar.TypeReg,
		})
		if err == nil {
			_, err = io.Copy(archive, file)
		}
		if closeErr := archive.Close(); err == nil {
			err = closeErr
		}
		_ = writer.CloseWithError(err)
		writeResult <- err
	}()
	execErr := h.executor.Exec(ctx, h.target, []string{
		defaultShellPath, "-c",
		"tar xf - -C " + shellquote.Join(parent),
	}, Streams{Stdin: reader, Stderr: io.Discard})
	if execErr != nil {
		_ = reader.CloseWithError(execErr)
	} else {
		_ = reader.Close()
	}
	writeErr := <-writeResult
	if execErr != nil {
		return execErr
	}
	return writeErr
}
