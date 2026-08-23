package filetransfer

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"errors"

	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/protocol/filestream"
)

func (manager *Manager) runDownload(
	ctx context.Context,
	taskID string,
	entry *activeTransfer,
) (_ filestream.TransferResult, resultErr error) {
	destination := entry.request.LocalPath
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o750); err != nil {
		return filestream.TransferResult{}, fmt.Errorf("create local destination directory: %w", err)
	}
	if err := validateDestination(destination, entry.request.Overwrite); err != nil {
		return filestream.TransferResult{}, err
	}
	if entry.request.Kind == fileTransferKindFile {
		return manager.runFileDownload(ctx, taskID, entry)
	}
	temporary, err := os.CreateTemp(parent, ".kubeloop-download-*.part")
	if err != nil {
		return filestream.TransferResult{}, fmt.Errorf("create local download temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		if err := os.Remove(temporaryPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			resultErr = errors.Join(resultErr, fmt.Errorf("remove local download temporary file: %w", err))
		}
	}()
	bounded := &boundedWriter{writer: temporary, maximum: manager.maximumBytes}
	_, result, transferErr := Download(ctx, manager.client, entry.profile, entry.session, remote.FileTransferSpec{
		Direction:  fileTransferDirectionDownload,
		Kind:       entry.request.Kind,
		Pod:        entry.request.Pod,
		Container:  entry.request.Container,
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
	defer func() {
		if err := os.RemoveAll(temporaryDirectory); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("remove local extraction directory: %w", err))
		}
	}()
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
	if err := manager.update(taskID, func(task *Task) {
		task.DoneBytes = offset
		if task.TotalBytes < offset {
			task.TotalBytes = offset
		}
	}); err != nil {
		_ = temporary.Close()
		return filestream.TransferResult{}, fmt.Errorf("checkpoint download metadata: %w", err)
	}
	bounded := &boundedWriter{writer: temporary, maximum: manager.maximumBytes, written: offset}
	_, result, transferErr := Download(ctx, manager.client, entry.profile, entry.session, remote.FileTransferSpec{
		Direction:  fileTransferDirectionDownload,
		Kind:       fileTransferKindFile,
		Pod:        entry.request.Pod,
		Container:  entry.request.Container,
		RemotePath: entry.request.RemotePath,
		Offset:     offset,
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

func hashLocalFile(ctx context.Context, filename string, maximum uint64) (_ uint64, _ [32]byte, resultErr error) {
	info, err := os.Lstat(filename)
	size, validSize := nonNegativeUint64(infoSize(info, err))
	if err != nil || !info.Mode().IsRegular() || !validSize || size > maximum {
		return 0, [32]byte{}, errors.New("downloaded temporary file is invalid")
	}
	file, err := os.Open(filename)
	if err != nil {
		return 0, [32]byte{}, err
	}
	defer func() {
		if err := file.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close downloaded temporary file: %w", err))
		}
	}()
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
	openedSize, validSize := nonNegativeUint64(opened.Size())
	if !validSize {
		return 0, [32]byte{}, errors.New("downloaded temporary file has an invalid size")
	}
	return openedSize, checksum, nil
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
	offset, validSize := nonNegativeUint64(opened.Size())
	if !validSize {
		_ = file.Close()
		return nil, 0, errors.New("resumable download temporary file has an invalid size")
	}
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
