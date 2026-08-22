package filetransfer

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"

	"errors"

	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/protocol/filestream"
)

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
	sizeChanged := previous.TotalBytes != 0 && previous.TotalBytes != size
	checksumChanged := previous.Checksum != "" && previous.Checksum != formattedChecksum
	if sizeChanged || checksumChanged {
		return filestream.TransferResult{}, errors.New("local upload source changed since the transfer started")
	}
	manager.update(taskID, func(task *Task) {
		task.TotalBytes = size
		task.Checksum = formattedChecksum
	})
	_, result, err := Upload(ctx, manager.client, entry.profile, entry.session, remote.FileTransferSpec{
		Direction:  fileTransferDirectionUpload,
		Kind:       entry.request.Kind,
		Pod:        entry.request.Pod,
		Container:  entry.request.Container,
		RemotePath: entry.request.RemotePath,
		Size:       size,
		Checksum:   filestream.FormatChecksum(checksum),
		Overwrite:  entry.request.Overwrite,
		ResumeID:   entry.resumeID,
	}, source, func(progress filestream.ProgressStatus) { manager.progress(taskID, progress) })
	return result, err
}
func (manager *Manager) prepareUpload(
	ctx context.Context,
	request Request,
) (*os.File, uint64, [32]byte, func(), error) {
	if request.Kind == fileTransferKindFile {
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
	openedSize, validSize := nonNegativeUint64(openedInfo.Size())
	if !validSize || openedSize == 0 || openedSize > maximum {
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
	return file, openedSize, checksum, nil
}
