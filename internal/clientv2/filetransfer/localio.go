package filetransfer

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/clientv2/remote"
	"github.com/fengqi-dev/kube-loop/internal/protocol/filestream"
	"github.com/google/uuid"
)

const maximumLocalArchiveEntries = 100_000

func (manager *Manager) runUpload(
	ctx context.Context,
	taskID string,
	entry *activeTransfer,
) (filestream.TransferResult, error) {
	source, size, checksum, cleanup, err := manager.prepareUpload(ctx, entry.request)
	if err != nil {
		return filestream.TransferResult{}, err
	}
	defer cleanup()
	previous := manager.task(taskID)
	formattedChecksum := filestream.FormatChecksum(checksum)
	if (previous.TotalBytes != 0 && previous.TotalBytes != size) || (previous.Checksum != "" && previous.Checksum != formattedChecksum) {
		return filestream.TransferResult{}, errors.New("local upload source changed since the transfer started")
	}
	manager.update(taskID, func(task *Task) {
		task.TotalBytes = size
		task.Checksum = formattedChecksum
	})
	_, result, err := Upload(ctx, manager.client, entry.profile, entry.session, remote.FileTransferSpec{
		Direction: "upload", Kind: entry.request.Kind, Pod: entry.request.Pod, Container: entry.request.Container,
		RemotePath: entry.request.RemotePath, Size: size, Checksum: filestream.FormatChecksum(checksum),
		Overwrite: entry.request.Overwrite, ResumeID: entry.resumeID,
	}, source, func(progress filestream.ProgressStatus) { manager.progress(taskID, progress) })
	return result, err
}

func (manager *Manager) runDownload(
	ctx context.Context,
	taskID string,
	entry *activeTransfer,
) (filestream.TransferResult, error) {
	destination := entry.request.LocalPath
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return filestream.TransferResult{}, fmt.Errorf("create local destination directory: %w", err)
	}
	if err := validateDestination(destination, entry.request.Overwrite); err != nil {
		return filestream.TransferResult{}, err
	}
	if entry.request.Kind == "file" {
		return manager.runFileDownload(ctx, taskID, entry)
	}
	temporary, err := os.CreateTemp(parent, ".kubeloop-download-*.part")
	if err != nil {
		return filestream.TransferResult{}, fmt.Errorf("create local download temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	bounded := &boundedWriter{writer: temporary, maximum: manager.maximumBytes}
	_, result, transferErr := Download(ctx, manager.client, entry.profile, entry.session, remote.FileTransferSpec{
		Direction: "download", Kind: entry.request.Kind, Pod: entry.request.Pod, Container: entry.request.Container,
		RemotePath: entry.request.RemotePath,
	}, bounded, func(progress filestream.ProgressStatus) { manager.progress(taskID, progress) })
	if transferErr != nil {
		_ = temporary.Close()
		return result, transferErr
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return result, fmt.Errorf("sync local download: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return result, fmt.Errorf("close local download: %w", err)
	}
	temporaryDirectory, err := os.MkdirTemp(parent, ".kubeloop-directory-*.part")
	if err != nil {
		return result, fmt.Errorf("create local extraction directory: %w", err)
	}
	defer os.RemoveAll(temporaryDirectory)
	archive, err := os.Open(temporaryPath)
	if err != nil {
		return result, fmt.Errorf("open downloaded directory archive: %w", err)
	}
	extractErr := extractArchive(ctx, archive, temporaryDirectory, manager.maximumBytes)
	closeErr := archive.Close()
	if extractErr != nil {
		return result, extractErr
	}
	if closeErr != nil {
		return result, closeErr
	}
	if err := publishLocalPath(temporaryDirectory, destination, entry.request.Overwrite); err != nil {
		return result, err
	}
	return result, nil
}

func (manager *Manager) runFileDownload(
	ctx context.Context,
	taskID string,
	entry *activeTransfer,
) (filestream.TransferResult, error) {
	temporary, offset, err := openPartialDownload(entry.temporaryPath, manager.maximumBytes)
	if err != nil {
		return filestream.TransferResult{}, err
	}
	manager.update(taskID, func(task *Task) {
		task.DoneBytes = offset
		if task.TotalBytes < offset {
			task.TotalBytes = offset
		}
	})
	bounded := &boundedWriter{writer: temporary, maximum: manager.maximumBytes, written: offset}
	_, result, transferErr := Download(ctx, manager.client, entry.profile, entry.session, remote.FileTransferSpec{
		Direction: "download", Kind: "file", Pod: entry.request.Pod, Container: entry.request.Container,
		RemotePath: entry.request.RemotePath, Offset: offset,
	}, bounded, func(progress filestream.ProgressStatus) { manager.progress(taskID, progress) })
	if transferErr != nil {
		_ = temporary.Close()
		return result, transferErr
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return result, fmt.Errorf("sync local download: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return result, fmt.Errorf("close local download: %w", err)
	}
	size, checksum, err := hashLocalFile(ctx, entry.temporaryPath, manager.maximumBytes)
	if err != nil || size != result.Transferred || !result.HasChecksum || checksum != result.Checksum {
		_ = os.Remove(entry.temporaryPath)
		if err != nil {
			return result, fmt.Errorf("verify resumed local download: %w", err)
		}
		return result, errors.New("resumed local download checksum does not match the Gateway result")
	}
	if err := publishLocalPath(entry.temporaryPath, entry.request.LocalPath, entry.request.Overwrite); err != nil {
		return result, err
	}
	return result, nil
}

func hashLocalFile(ctx context.Context, filename string, maximum uint64) (uint64, [32]byte, error) {
	info, err := os.Lstat(filename)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 0 || uint64(info.Size()) > maximum {
		return 0, [32]byte{}, errors.New("downloaded temporary file is invalid")
	}
	file, err := os.Open(filename)
	if err != nil {
		return 0, [32]byte{}, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return 0, [32]byte{}, errors.New("downloaded temporary file changed while opening")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, &contextReader{ctx: ctx, reader: file}); err != nil {
		return 0, [32]byte{}, err
	}
	var checksum [32]byte
	copy(checksum[:], hash.Sum(nil))
	return uint64(opened.Size()), checksum, nil
}

func downloadTemporaryPath(destination, taskID string) string {
	return filepath.Join(filepath.Dir(destination), ".kubeloop-download-"+taskID+".part")
}

func openPartialDownload(filename string, maximum uint64) (*os.File, uint64, error) {
	if filename == "" || !filepath.IsAbs(filename) {
		return nil, 0, errors.New("resumable download temporary path is invalid")
	}
	info, statErr := os.Lstat(filename)
	if statErr == nil && !info.Mode().IsRegular() {
		return nil, 0, errors.New("resumable download temporary path must be a regular file")
	}
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return nil, 0, statErr
	}
	flags := os.O_RDWR
	if errors.Is(statErr, os.ErrNotExist) {
		flags |= os.O_CREATE | os.O_EXCL
	}
	file, err := os.OpenFile(filename, flags, 0o600)
	if err != nil {
		return nil, 0, fmt.Errorf("open resumable download temporary file: %w", err)
	}
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || (info != nil && !os.SameFile(info, opened)) {
		_ = file.Close()
		return nil, 0, errors.New("resumable download temporary file changed while opening")
	}
	offset := uint64(opened.Size())
	if offset > maximum {
		if err := file.Truncate(0); err != nil {
			_ = file.Close()
			return nil, 0, err
		}
		offset = 0
	}
	if _, err := file.Seek(int64(offset), io.SeekStart); err != nil {
		_ = file.Close()
		return nil, 0, err
	}
	return file, offset, nil
}

func (manager *Manager) prepareUpload(
	ctx context.Context,
	request Request,
) (*os.File, uint64, [32]byte, func(), error) {
	if request.Kind == "file" {
		file, size, checksum, err := openUploadFile(ctx, request.LocalPath, manager.maximumBytes)
		if err != nil {
			return nil, 0, [32]byte{}, func() {}, err
		}
		return file, size, checksum, func() { _ = file.Close() }, nil
	}
	temporaryRoot := manager.temporaryDir
	if temporaryRoot == "" {
		temporaryRoot = os.TempDir()
	}
	if err := os.MkdirAll(temporaryRoot, 0o700); err != nil {
		return nil, 0, [32]byte{}, func() {}, fmt.Errorf("create file transfer temporary directory: %w", err)
	}
	archive, err := os.CreateTemp(temporaryRoot, "kubeloop-upload-*.tar")
	if err != nil {
		return nil, 0, [32]byte{}, func() {}, err
	}
	cleanup := func() {
		_ = archive.Close()
		_ = os.Remove(archive.Name())
	}
	checksum, size, err := createArchive(ctx, request.LocalPath, archive, manager.maximumBytes)
	if err != nil {
		cleanup()
		return nil, 0, [32]byte{}, func() {}, err
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return nil, 0, [32]byte{}, func() {}, err
	}
	return archive, size, checksum, cleanup, nil
}

func cleanLocalPath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.IndexByte(value, 0) >= 0 || !filepath.IsAbs(value) {
		return "", errors.New("local file transfer path is required")
	}
	absolute, err := filepath.Abs(filepath.Clean(value))
	if err != nil || absolute == filepath.VolumeName(absolute)+string(filepath.Separator) {
		return "", errors.New("local file transfer path is invalid")
	}
	return absolute, nil
}

func openUploadFile(ctx context.Context, filename string, maximum uint64) (*os.File, uint64, [32]byte, error) {
	info, err := os.Lstat(filename)
	if err != nil {
		return nil, 0, [32]byte{}, fmt.Errorf("inspect local upload file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, 0, [32]byte{}, errors.New("local upload path must be a regular file")
	}
	file, err := os.Open(filename)
	if err != nil {
		return nil, 0, [32]byte{}, fmt.Errorf("open local upload file: %w", err)
	}
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		_ = file.Close()
		return nil, 0, [32]byte{}, errors.New("local upload file changed while opening")
	}
	if openedInfo.Size() <= 0 || uint64(openedInfo.Size()) > maximum {
		_ = file.Close()
		return nil, 0, [32]byte{}, errors.New("local upload file exceeds the configured size limit")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, &contextReader{ctx: ctx, reader: file}); err != nil {
		_ = file.Close()
		return nil, 0, [32]byte{}, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, 0, [32]byte{}, err
	}
	var checksum [32]byte
	copy(checksum[:], hash.Sum(nil))
	return file, uint64(openedInfo.Size()), checksum, nil
}

func createArchive(ctx context.Context, root string, destination *os.File, maximum uint64) ([32]byte, uint64, error) {
	rootInfo, err := os.Lstat(root)
	if err != nil || !rootInfo.IsDir() {
		return [32]byte{}, 0, errors.New("local directory upload source is not a directory")
	}
	hash := sha256.New()
	bounded := &boundedWriter{writer: io.MultiWriter(destination, hash), maximum: maximum}
	archive := tar.NewWriter(bounded)
	entries := 0
	walkErr := filepath.WalkDir(root, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		entries++
		if entries > maximumLocalArchiveEntries {
			return errors.New("local directory contains too many entries")
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("local directory contains an unsupported path: %s", filename)
		}
		relative, err := filepath.Rel(root, filename)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(relative)
		if name == "" {
			name = "."
		}
		header := &tar.Header{
			Name: name, Mode: int64(info.Mode().Perm()), ModTime: info.ModTime().UTC().Truncate(time.Second),
		}
		if info.IsDir() {
			header.Typeflag = tar.TypeDir
		} else {
			header.Typeflag = tar.TypeReg
			header.Size = info.Size()
		}
		if err := archive.WriteHeader(header); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		file, err := os.Open(filename)
		if err != nil {
			return err
		}
		openedInfo, statErr := file.Stat()
		if statErr != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
			_ = file.Close()
			return errors.New("local directory changed while creating its upload snapshot")
		}
		_, copyErr := io.CopyN(archive, &contextReader{ctx: ctx, reader: file}, info.Size())
		closeErr := file.Close()
		return errors.Join(copyErr, closeErr)
	})
	closeErr := archive.Close()
	if walkErr != nil || closeErr != nil {
		return [32]byte{}, 0, errors.Join(walkErr, closeErr)
	}
	if bounded.written == 0 {
		return [32]byte{}, 0, errors.New("local directory archive is empty")
	}
	if err := destination.Sync(); err != nil {
		return [32]byte{}, 0, err
	}
	var checksum [32]byte
	copy(checksum[:], hash.Sum(nil))
	return checksum, bounded.written, nil
}

func extractArchive(ctx context.Context, input io.Reader, root string, maximum uint64) error {
	archive := tar.NewReader(input)
	var total uint64
	type directoryPermission struct {
		path string
		mode os.FileMode
	}
	directories := make([]directoryPermission, 0)
	seen := make(map[string]struct{})
	for entries := 0; ; entries++ {
		if entries >= maximumLocalArchiveEntries {
			return errors.New("downloaded directory contains too many entries")
		}
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			for index := len(directories) - 1; index >= 0; index-- {
				if err := os.Chmod(directories[index].path, directories[index].mode); err != nil {
					return err
				}
			}
			return nil
		}
		if err != nil {
			return errors.New("downloaded directory archive is invalid")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		name := strings.ReplaceAll(header.Name, "\\", "/")
		cleaned := path.Clean(name)
		if name == "" || path.IsAbs(name) || cleaned == ".." || strings.HasPrefix(cleaned, "../") ||
			containsParentPathComponent(name) ||
			(header.Typeflag != tar.TypeDir && header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA) ||
			header.Size < 0 || total > maximum || uint64(header.Size) > maximum-total {
			return errors.New("downloaded directory archive contains an unsafe entry")
		}
		total += uint64(header.Size)
		if _, exists := seen[cleaned]; exists {
			return errors.New("downloaded directory archive contains a duplicate entry")
		}
		seen[cleaned] = struct{}{}
		mode := os.FileMode(header.Mode) & 0o777
		if cleaned == "." {
			if header.Typeflag != tar.TypeDir {
				return errors.New("downloaded directory root entry is invalid")
			}
			directories = append(directories, directoryPermission{path: root, mode: mode})
			continue
		}
		target := filepath.Join(root, filepath.FromSlash(cleaned))
		relative, err := filepath.Rel(root, target)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return errors.New("downloaded directory path escapes its destination")
		}
		if header.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(target, mode|0o700); err != nil {
				return err
			}
			directories = append(directories, directoryPermission{path: target, mode: mode})
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			return err
		}
		_, copyErr := io.CopyN(file, &contextReader{ctx: ctx, reader: archive}, header.Size)
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil {
			return errors.Join(copyErr, closeErr)
		}
	}
}

func containsParentPathComponent(value string) bool {
	for component := range strings.SplitSeq(value, "/") {
		if component == ".." {
			return true
		}
	}
	return false
}

func validateDestination(destination string, overwrite bool) error {
	_, err := os.Lstat(destination)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		return err
	case !overwrite:
		return errors.New("local download destination already exists")
	default:
		return nil
	}
}

func publishLocalPath(temporary, destination string, overwrite bool) error {
	if err := validateDestination(destination, overwrite); err != nil {
		return err
	}
	if _, err := os.Lstat(destination); errors.Is(err, os.ErrNotExist) {
		if err := os.Rename(temporary, destination); err != nil {
			return fmt.Errorf("publish local download: %w", err)
		}
		return nil
	} else if err != nil {
		return err
	}
	backup := destination + ".kubeloop-backup-" + uuid.NewString()
	if err := os.Rename(destination, backup); err != nil {
		return fmt.Errorf("stage existing local destination: %w", err)
	}
	if err := os.Rename(temporary, destination); err != nil {
		if rollbackErr := os.Rename(backup, destination); rollbackErr != nil {
			return errors.Join(fmt.Errorf("publish local download: %w", err), fmt.Errorf("restore previous destination: %w", rollbackErr))
		}
		return fmt.Errorf("publish local download: %w", err)
	}
	if err := os.RemoveAll(backup); err != nil {
		return fmt.Errorf("remove replaced local destination: %w", err)
	}
	return nil
}

type boundedWriter struct {
	writer  io.Writer
	maximum uint64
	written uint64
}

func (writer *boundedWriter) Write(value []byte) (int, error) {
	if writer.written > writer.maximum || uint64(len(value)) > writer.maximum-writer.written {
		return 0, errors.New("file transfer exceeds the configured local size limit")
	}
	n, err := writer.writer.Write(value)
	writer.written += uint64(n)
	return n, err
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader *contextReader) Read(value []byte) (int, error) {
	select {
	case <-reader.ctx.Done():
		return 0, reader.ctx.Err()
	default:
		return reader.reader.Read(value)
	}
}
