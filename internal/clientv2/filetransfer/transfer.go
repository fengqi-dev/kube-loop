package filetransfer

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/coder/websocket"
	"github.com/fengqi-dev/kube-loop/internal/clientv2/profile"
	"github.com/fengqi-dev/kube-loop/internal/clientv2/remote"
	"github.com/fengqi-dev/kube-loop/internal/protocol/filestream"
	"github.com/google/uuid"
)

type Client interface {
	CreateFileTransferTask(context.Context, profile.Profile, remote.Session, remote.FileTransferSpec, string) (remote.FileTransferTask, error)
	OpenFileTransferStream(context.Context, profile.Profile, remote.Session, remote.FileTransferTask) (*websocket.Conn, error)
}

type ProgressFunc func(filestream.ProgressStatus)

type streamResponse struct {
	result filestream.TransferResult
	err    error
}

func Upload(
	ctx context.Context,
	client Client,
	serverProfile profile.Profile,
	session remote.Session,
	spec remote.FileTransferSpec,
	input io.Reader,
	onProgress ProgressFunc,
) (remote.FileTransferTask, filestream.TransferResult, error) {
	if client == nil || input == nil {
		return remote.FileTransferTask{}, filestream.TransferResult{}, errors.New("file upload client and input are required")
	}
	if spec.Direction != "upload" || spec.Size == 0 || spec.Offset > spec.Size {
		return remote.FileTransferTask{}, filestream.TransferResult{}, errors.New("valid file upload specification is required")
	}
	task, err := client.CreateFileTransferTask(ctx, serverProfile, session, spec, "file-upload:"+uuid.NewString())
	if err != nil {
		return remote.FileTransferTask{}, filestream.TransferResult{}, err
	}
	if task.Offset > spec.Size || task.ResumeID != spec.ResumeID {
		return task, filestream.TransferResult{}, errors.New("Gateway returned an invalid resumable upload offset")
	}
	if task.Offset > 0 {
		seeker, ok := input.(io.Seeker)
		if !ok {
			return task, filestream.TransferResult{}, errors.New("resumable upload input is not seekable")
		}
		if _, err := seeker.Seek(int64(task.Offset), io.SeekStart); err != nil {
			return task, filestream.TransferResult{}, fmt.Errorf("seek resumable upload input: %w", err)
		}
	}
	spec.Offset = task.Offset
	connection, err := client.OpenFileTransferStream(ctx, serverProfile, session, task)
	if err != nil {
		return task, filestream.TransferResult{}, err
	}
	defer connection.CloseNow()
	connection.SetReadLimit(filestream.MaximumData + 1)
	responses := make(chan streamResponse, 1)
	go func() { responses <- readUploadResponses(ctx, connection, onProgress) }()
	remaining := spec.Size - spec.Offset
	buffer := make([]byte, min(uint64(filestream.MaximumData), remaining))
	for remaining > 0 {
		length := min(uint64(len(buffer)), remaining)
		if _, err := io.ReadFull(input, buffer[:length]); err != nil {
			cancel(connection)
			return task, filestream.TransferResult{}, fmt.Errorf("read local upload content: %w", err)
		}
		encoded, _ := filestream.Encode(filestream.Frame{Type: filestream.Data, Payload: buffer[:length]})
		if err := connection.Write(ctx, websocket.MessageBinary, encoded); err != nil {
			return uploadWriteResult(task, responses, err)
		}
		remaining -= length
	}
	complete, _ := filestream.Encode(filestream.Frame{Type: filestream.Complete})
	if err := connection.Write(ctx, websocket.MessageBinary, complete); err != nil {
		return uploadWriteResult(task, responses, err)
	}
	response := <-responses
	if response.err != nil {
		return task, response.result, response.err
	}
	expected, _ := filestream.ParseChecksum(spec.Checksum)
	if response.result.Status == filestream.ResultSucceeded &&
		(response.result.Transferred != spec.Size || !response.result.HasChecksum || response.result.Checksum != expected) {
		return task, response.result, errors.New("Gateway returned a mismatched file upload result")
	}
	return task, response.result, resultError(response.result)
}

func Download(
	ctx context.Context,
	client Client,
	serverProfile profile.Profile,
	session remote.Session,
	spec remote.FileTransferSpec,
	output io.Writer,
	onProgress ProgressFunc,
) (remote.FileTransferTask, filestream.TransferResult, error) {
	if client == nil || output == nil {
		return remote.FileTransferTask{}, filestream.TransferResult{}, errors.New("file download client and output are required")
	}
	if spec.Direction != "download" {
		return remote.FileTransferTask{}, filestream.TransferResult{}, errors.New("valid file download specification is required")
	}
	task, err := client.CreateFileTransferTask(ctx, serverProfile, session, spec, "file-download:"+uuid.NewString())
	if err != nil {
		return remote.FileTransferTask{}, filestream.TransferResult{}, err
	}
	if task.Offset != spec.Offset {
		return task, filestream.TransferResult{}, errors.New("Gateway returned a mismatched file download offset")
	}
	connection, err := client.OpenFileTransferStream(ctx, serverProfile, session, task)
	if err != nil {
		return task, filestream.TransferResult{}, err
	}
	defer connection.CloseNow()
	connection.SetReadLimit(filestream.MaximumData + 1)
	hash := sha256.New()
	destination := output
	if spec.Offset == 0 {
		destination = io.MultiWriter(output, hash)
	}
	transferred := spec.Offset
	for {
		messageType, encoded, err := connection.Read(ctx)
		if err != nil {
			return task, filestream.TransferResult{}, fmt.Errorf("read Gateway file download stream: %w", err)
		}
		if messageType != websocket.MessageBinary {
			cancel(connection)
			return task, filestream.TransferResult{}, errors.New("Gateway file download stream returned a non-binary message")
		}
		frame, err := filestream.Decode(encoded)
		if err != nil {
			cancel(connection)
			return task, filestream.TransferResult{}, err
		}
		switch frame.Type {
		case filestream.Data:
			written, writeErr := destination.Write(frame.Payload)
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
				return task, result, errors.New("Gateway returned a mismatched file download byte count")
			}
			if result.Status == filestream.ResultSucceeded && spec.Offset == 0 &&
				(!result.HasChecksum || !equalDigest(result.Checksum, hash.Sum(nil))) {
				return task, result, errors.New("Gateway returned a mismatched file download checksum")
			}
			return task, result, resultError(result)
		default:
			cancel(connection)
			return task, filestream.TransferResult{}, errors.New("Gateway sent a client-only file download frame")
		}
	}
}

func readUploadResponses(ctx context.Context, connection *websocket.Conn, onProgress ProgressFunc) streamResponse {
	for {
		messageType, encoded, err := connection.Read(ctx)
		if err != nil {
			return streamResponse{err: fmt.Errorf("read Gateway file upload stream: %w", err)}
		}
		if messageType != websocket.MessageBinary {
			return streamResponse{err: errors.New("Gateway file upload stream returned a non-binary message")}
		}
		frame, err := filestream.Decode(encoded)
		if err != nil {
			return streamResponse{err: err}
		}
		switch frame.Type {
		case filestream.Progress:
			progress, err := filestream.DecodeProgress(frame)
			if err != nil {
				return streamResponse{err: err}
			}
			if onProgress != nil {
				onProgress(progress)
			}
		case filestream.Result:
			result, err := filestream.DecodeResult(frame)
			return streamResponse{result: result, err: err}
		default:
			return streamResponse{err: errors.New("Gateway sent a client-only file upload frame")}
		}
	}
}

func uploadWriteResult(task remote.FileTransferTask, responses <-chan streamResponse, writeErr error) (remote.FileTransferTask, filestream.TransferResult, error) {
	select {
	case response := <-responses:
		if response.err != nil {
			return task, response.result, response.err
		}
		return task, response.result, resultError(response.result)
	default:
		return task, filestream.TransferResult{}, fmt.Errorf("write Gateway file upload stream: %w", writeErr)
	}
}

func cancel(connection *websocket.Conn) {
	encoded, _ := filestream.Encode(filestream.Frame{Type: filestream.Cancel})
	ctx, stop := context.WithTimeout(context.Background(), 2*time.Second)
	defer stop()
	_ = connection.Write(ctx, websocket.MessageBinary, encoded)
}

func resultError(result filestream.TransferResult) error {
	switch result.Status {
	case filestream.ResultSucceeded:
		return nil
	case filestream.ResultCancelled:
		return context.Canceled
	default:
		if result.Error == "" {
			return errors.New("Gateway file transfer failed")
		}
		return errors.New(result.Error)
	}
}

func equalDigest(checksum [32]byte, value []byte) bool {
	return len(value) == len(checksum) && string(checksum[:]) == string(value)
}
