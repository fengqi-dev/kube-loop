package filetransfer

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/protocol/filestream"
)

func Download(
	ctx context.Context,
	client Client,
	serverProfile profile.Profile,
	session remote.Session,
	spec remote.FileTransferSpec,
	output io.Writer,
	onProgress ProgressFunc,
) (_ remote.FileTransferTask, _ filestream.TransferResult, resultErr error) {
	if client == nil || output == nil {
		return remote.FileTransferTask{}, filestream.TransferResult{}, errors.New(
			"file download client and output are required",
		)
	}
	if spec.Direction != fileTransferDirectionDownload {
		return remote.FileTransferTask{}, filestream.TransferResult{}, errors.New(
			"valid file download specification is required",
		)
	}
	task, err := client.CreateFileTransferTask(ctx, serverProfile, session, spec, "file-download:"+uuid.NewString())
	if err != nil {
		return remote.FileTransferTask{}, filestream.TransferResult{}, err
	}
	if task.Offset != spec.Offset {
		return task, filestream.TransferResult{}, errors.New("gateway returned a mismatched file download offset")
	}
	connection, err := client.OpenFileTransferStream(ctx, serverProfile, session, task)
	if err != nil {
		return task, filestream.TransferResult{}, err
	}
	defer func() {
		resultErr = errors.Join(resultErr, connection.Close())
	}()
	connection.SetReadLimit(filestream.MaximumData + 1)
	hash := sha256.New()
	destination := output
	if spec.Offset == 0 {
		destination = io.MultiWriter(output, hash)
	}
	transferred := spec.Offset
	for {
		messageType, encoded, err := readWebSocket(ctx, connection)
		if err != nil {
			return task, filestream.TransferResult{}, fmt.Errorf("read Gateway file download stream: %w", err)
		}
		if messageType != websocket.BinaryMessage {
			cancel(connection)
			return task, filestream.TransferResult{}, errors.New(
				"gateway file download stream returned a non-binary message",
			)
		}
		frame, err := filestream.Decode(encoded)
		if err != nil {
			cancel(connection)
			return task, filestream.TransferResult{}, err
		}
		switch frame.Type {
		case filestream.Data:
			written, writeErr := destination.Write(frame.Payload)
			if written < 0 {
				cancel(connection)
				return task, filestream.TransferResult{}, io.ErrShortWrite
			}
			transferred += uint64(written)
			if writeErr != nil || written != len(frame.Payload) {
				cancel(connection)
				return task, filestream.TransferResult{}, errors.New("write local download content")
			}
		case filestream.Progress:
			progress, decodeErr := filestream.DecodeProgress(frame)
			if decodeErr != nil {
				cancel(connection)
				return task, filestream.TransferResult{}, decodeErr
			}
			if onProgress != nil {
				onProgress(progress)
			}
		case filestream.Result:
			result, decodeErr := filestream.DecodeResult(frame)
			if decodeErr != nil {
				return task, filestream.TransferResult{}, decodeErr
			}
			if result.Transferred != transferred {
				return task, result, errors.New("gateway returned a mismatched file download byte count")
			}
			if result.Status == filestream.ResultSucceeded && spec.Offset == 0 &&
				(!result.HasChecksum || !equalDigest(result.Checksum, hash.Sum(nil))) {
				return task, result, errors.New("gateway returned a mismatched file download checksum")
			}
			return task, result, resultError(result)
		default:
			cancel(connection)
			return task, filestream.TransferResult{}, errors.New("gateway sent a client-only file download frame")
		}
	}
}
