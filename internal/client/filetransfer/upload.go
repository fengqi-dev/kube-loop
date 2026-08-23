package filetransfer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/protocol/filestream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/websocket"
)

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
) (_ remote.FileTransferTask, _ filestream.TransferResult, resultErr error) {
	if client == nil || input == nil {
		return remote.FileTransferTask{}, filestream.TransferResult{}, errors.New(
			"file upload client and input are required",
		)
	}
	if spec.Direction != fileTransferDirectionUpload || spec.Size == 0 || spec.Offset > spec.Size {
		return remote.FileTransferTask{}, filestream.TransferResult{}, errors.New(
			"valid file upload specification is required",
		)
	}
	task, err := client.CreateFileTransferTask(ctx, serverProfile, session, spec, "file-upload:"+uuid.NewString())
	if err != nil {
		return remote.FileTransferTask{}, filestream.TransferResult{}, err
	}
	if task.Offset > spec.Size || task.ResumeID != spec.ResumeID {
		return task, filestream.TransferResult{}, errors.New("gateway returned an invalid resumable upload offset")
	}
	if task.Offset > 0 {
		seeker, ok := input.(io.Seeker)
		if !ok {
			return task, filestream.TransferResult{}, errors.New("resumable upload input is not seekable")
		}
		if task.Offset > math.MaxInt64 {
			return task, filestream.TransferResult{}, errors.New("resumable upload offset exceeds local seek range")
		}
		offset := int64(task.Offset)
		if _, err := seeker.Seek(offset, io.SeekStart); err != nil {
			return task, filestream.TransferResult{}, fmt.Errorf("seek resumable upload input: %w", err)
		}
	}
	spec.Offset = task.Offset
	connection, err := client.OpenFileTransferStream(ctx, serverProfile, session, task)
	if err != nil {
		return task, filestream.TransferResult{}, err
	}
	defer func() {
		resultErr = errors.Join(resultErr, connection.CloseNow())
	}()
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
		return task, response.result, errors.New("gateway returned a mismatched file upload result")
	}
	return task, response.result, resultError(response.result)
}

func readUploadResponses(ctx context.Context, connection *websocket.Conn, onProgress ProgressFunc) streamResponse {
	for {
		messageType, encoded, err := connection.Read(ctx)
		if err != nil {
			return streamResponse{err: fmt.Errorf("read Gateway file upload stream: %w", err)}
		}
		if messageType != websocket.MessageBinary {
			return streamResponse{err: errors.New("gateway file upload stream returned a non-binary message")}
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
			return streamResponse{err: errors.New("gateway sent a client-only file upload frame")}
		}
	}
}

func uploadWriteResult(
	task remote.FileTransferTask,
	responses <-chan streamResponse,
	writeErr error,
) (remote.FileTransferTask, filestream.TransferResult, error) {
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
