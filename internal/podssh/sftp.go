package podssh

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
	"sync"
	"time"

	"github.com/pkg/sftp"
)

type sftpHandler struct {
	executor Executor
	target   Target
}

func newSFTPHandler(executor Executor, target Target) *sftpHandler {
	return &sftpHandler{executor: executor, target: target}
}

func (h *sftpHandler) Fileread(request *sftp.Request) (io.ReaderAt, error) {
	file, err := h.downloadFile(request.Context(), cleanRemotePath(request.Filepath))
	if err != nil {
		return nil, err
	}
	return &downloadFile{File: file}, nil
}

func (h *sftpHandler) Filewrite(request *sftp.Request) (io.WriterAt, error) {
	remotePath := cleanRemotePath(request.Filepath)
	flags := request.Pflags()
	if flags.Excl {
		if _, err := h.stat(request.Context(), remotePath); err == nil {
			return nil, os.ErrExist
		}
	}
	var file *os.File
	var err error
	if !flags.Trunc {
		file, err = h.downloadFile(request.Context(), remotePath)
	} else {
		file, err = os.CreateTemp("", "kubeloop-sftp-upload-*")
	}
	if err != nil {
		// OpenSSH scp opens uploads with WRITE|CREAT but without TRUNC.
		// A missing Pod file is reported by Kubernetes exec as a generic
		// command error, so it cannot reliably satisfy os.ErrNotExist.
		if flags.Creat {
			file, err = os.CreateTemp("", "kubeloop-sftp-upload-*")
		}
		if err != nil {
			return nil, err
		}
	}
	if flags.Trunc {
		if err := file.Truncate(0); err != nil {
			_ = file.Close()
			_ = os.Remove(file.Name())
			return nil, err
		}
	}
	mode := os.FileMode(0o644)
	if attrs := request.Attributes(); attrs != nil && request.AttrFlags().Permissions {
		mode = attrs.FileMode().Perm()
	}
	return &uploadFile{
		File: file,
		closeRemote: func() error {
			return h.upload(request.Context(), remotePath, file, mode)
		},
	}, nil
}

func (h *sftpHandler) Filelist(request *sftp.Request) (sftp.ListerAt, error) {
	remotePath := cleanRemotePath(request.Filepath)
	switch request.Method {
	case "List":
		items, err := h.list(request.Context(), remotePath)
		if err != nil {
			return nil, err
		}
		return fileInfoList(items), nil
	case "Stat", "Lstat", "Readlink":
		item, err := h.stat(request.Context(), remotePath)
		if err != nil {
			return nil, err
		}
		return fileInfoList([]os.FileInfo{item}), nil
	default:
		return nil, fmt.Errorf("unsupported SFTP list operation %q", request.Method)
	}
}

func (h *sftpHandler) Lstat(request *sftp.Request) (sftp.ListerAt, error) {
	item, err := h.stat(request.Context(), cleanRemotePath(request.Filepath))
	if err != nil {
		return nil, err
	}
	return fileInfoList([]os.FileInfo{item}), nil
}

func (h *sftpHandler) RealPath(raw string) (string, error) {
	return cleanRemotePath(raw), nil
}

func (h *sftpHandler) Readlink(raw string) (string, error) {
	var stdout bytes.Buffer
	err := h.exec(context.Background(), "readlink "+shellQuote(cleanRemotePath(raw)), nil, &stdout)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}

func (h *sftpHandler) Filecmd(request *sftp.Request) error {
	remotePath := cleanRemotePath(request.Filepath)
	var command string
	switch request.Method {
	case "Mkdir":
		command = "mkdir -- " + shellQuote(remotePath)
	case "Rmdir":
		command = "rmdir -- " + shellQuote(remotePath)
	case "Remove":
		command = "rm -f -- " + shellQuote(remotePath)
	case "Rename", "PosixRename":
		command = "mv -- " + shellQuote(remotePath) + " " + shellQuote(cleanRemotePath(request.Target))
	case "Symlink":
		command = "ln -s -- " + shellQuote(request.Filepath) + " " + shellQuote(cleanRemotePath(request.Target))
	case "Link":
		command = "ln -- " + shellQuote(remotePath) + " " + shellQuote(cleanRemotePath(request.Target))
	case "Setstat":
		return h.setstat(request.Context(), remotePath, request)
	default:
		return fmt.Errorf("unsupported SFTP command %q", request.Method)
	}
	return h.exec(request.Context(), command, nil, io.Discard)
}

func (h *sftpHandler) PosixRename(request *sftp.Request) error {
	return h.Filecmd(request)
}

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
			"/bin/sh", "-c",
			"tar cf - -C " + shellQuote(parent) + " " + shellQuote(base),
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
		"/bin/sh", "-c",
		"tar xf - -C " + shellQuote(parent),
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

func (h *sftpHandler) stat(ctx context.Context, remotePath string) (os.FileInfo, error) {
	parent, base := splitRemotePath(remotePath)
	reader, writer := io.Pipe()
	execResult := make(chan error, 1)
	go func() {
		execResult <- h.executor.Exec(ctx, h.target, []string{
			"/bin/sh", "-c",
			"tar cf - --no-recursion -C " + shellQuote(parent) + " " + shellQuote(base),
		}, Streams{Stdout: writer, Stderr: io.Discard})
		_ = writer.Close()
	}()
	archive := tar.NewReader(reader)
	header, readErr := archive.Next()
	if readErr == nil {
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
	if err := h.exec(ctx, "ls -A1 -- "+shellQuote(remotePath), nil, &stdout); err != nil {
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
			"chmod "+strconv.FormatUint(uint64(attrs.FileMode().Perm()), 8)+" -- "+shellQuote(remotePath),
		)
	}
	if flags.Size {
		commands = append(commands,
			"truncate -s "+strconv.FormatUint(attrs.Size, 10)+" -- "+shellQuote(remotePath),
		)
	}
	if flags.Acmodtime {
		commands = append(commands,
			"touch -m -d @"+strconv.FormatUint(uint64(attrs.Mtime), 10)+" -- "+shellQuote(remotePath),
		)
	}
	if len(commands) == 0 {
		return nil
	}
	return h.exec(ctx, strings.Join(commands, " && "), nil, io.Discard)
}

func (h *sftpHandler) exec(
	ctx context.Context,
	script string,
	stdin io.Reader,
	stdout io.Writer,
) error {
	var stderr bytes.Buffer
	if stdout == nil {
		stdout = io.Discard
	}
	err := h.executor.Exec(ctx, h.target, []string{"/bin/sh", "-c", script}, Streams{
		Stdin: stdin, Stdout: stdout, Stderr: &stderr,
	})
	if err != nil && strings.TrimSpace(stderr.String()) != "" {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return err
}

type uploadFile struct {
	*os.File
	closeRemote func() error
	once        sync.Once
	closeErr    error
}

type downloadFile struct {
	*os.File
	once sync.Once
}

func (f *downloadFile) Close() error {
	var err error
	f.once.Do(func() {
		err = f.File.Close()
		_ = os.Remove(f.File.Name())
	})
	return err
}

func (f *uploadFile) Close() error {
	f.once.Do(func() {
		if err := f.File.Sync(); err != nil {
			f.closeErr = err
		} else if err := f.closeRemote(); err != nil {
			f.closeErr = err
		}
		if err := f.File.Close(); f.closeErr == nil {
			f.closeErr = err
		}
		_ = os.Remove(f.File.Name())
	})
	return f.closeErr
}

type fileInfoList []os.FileInfo

func (items fileInfoList) ListAt(destination []os.FileInfo, offset int64) (int, error) {
	if offset >= int64(len(items)) {
		return 0, io.EOF
	}
	count := copy(destination, items[offset:])
	if int(offset)+count >= len(items) {
		return count, io.EOF
	}
	return count, nil
}

func cleanRemotePath(raw string) string {
	cleaned := path.Clean("/" + strings.TrimSpace(raw))
	if cleaned == "." || cleaned == "" {
		return "/"
	}
	return cleaned
}

func splitRemotePath(remotePath string) (string, string) {
	remotePath = cleanRemotePath(remotePath)
	if remotePath == "/" {
		return "/", "."
	}
	return path.Dir(remotePath), path.Base(remotePath)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func archiveNameMatches(name, base string) bool {
	name = strings.TrimPrefix(path.Clean("/"+name), "/")
	base = strings.TrimPrefix(path.Clean("/"+base), "/")
	return name == base
}
