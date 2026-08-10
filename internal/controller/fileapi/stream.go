package fileapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/fengqi-dev/kube-loop/internal/controller"
	"github.com/fengqi-dev/kube-loop/internal/controller/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controller/streamlease"
	"github.com/fengqi-dev/kube-loop/internal/protocol/filestream"
	"github.com/fengqi-dev/kube-loop/internal/remotetask"
	"github.com/google/uuid"
)

func (handler *Handler) stream(
	writer http.ResponseWriter,
	request *http.Request,
	principal controller.Principal,
	session sessionapi.ActiveSession,
	taskID string,
) *controller.APIError {
	if _, err := uuid.Parse(taskID); err != nil {
		return notFound()
	}
	task, err := handler.storage.Tasks().GetByID(request.Context(), taskID)
	if err != nil || !owned(task, principal, session) {
		return notFound()
	}
	if task.State != remotetask.Pending {
		return &controller.APIError{Code: controller.CodeConflict, Message: "file transfer Task was already claimed"}
	}
	spec, err := handler.specFromTask(task)
	if err != nil {
		return internalError(err)
	}
	if err := handler.storage.Tasks().UpdateState(request.Context(), task.ID, remotetask.Pending, remotetask.Starting, json.RawMessage(`{}`), handler.now().UTC()); err != nil {
		return storageError(err)
	}
	connection, err := websocket.Accept(writer, request, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		handler.persistState(task.ID, remotetask.Starting, remotetask.Failed, streamResult{Error: "WebSocket upgrade failed"})
		return nil
	}
	defer connection.CloseNow()
	connection.SetReadLimit(filestream.MaximumData + 1)
	leaseContext, cancel, err := streamlease.Start(request.Context(), handler.storage, principal, session, streamlease.Config{
		Now: handler.now, CheckInterval: handler.credentialCheckInterval,
		Runtime: streamlease.RuntimeFrom(handler.sessions), TaskID: task.ID, HeartbeatTask: true,
	})
	if err != nil {
		handler.persistState(task.ID, remotetask.Starting, remotetask.Failed, streamResult{Error: "authorization lease expired"})
		_ = connection.Close(websocket.StatusPolicyViolation, "authorization lease expired")
		return nil
	}
	defer cancel()
	if err := handler.persistState(task.ID, remotetask.Starting, remotetask.Running, streamResult{}); err != nil {
		_ = connection.Close(websocket.StatusInternalError, "file transfer state persistence failed")
		return nil
	}
	var writeMu sync.Mutex
	var outcome Outcome
	var transferErr error
	if spec.Direction == DirectionUpload {
		outcome, transferErr = handler.upload(
			leaseContext, cancel, connection, &writeMu, principal, session.Namespace, task.ID, spec,
		)
	} else {
		outcome, transferErr = handler.download(
			leaseContext, request.Context(), cancel, connection, &writeMu, principal, session.Namespace, task.ID, spec,
		)
	}
	cancelled := leaseContext.Err() != nil || errors.Is(transferErr, context.Canceled)
	result := resultFromOutcome(outcome, transferErr, cancelled)
	nextState := remotetask.Stopped
	if transferErr != nil && !cancelled {
		nextState = remotetask.Failed
	}
	if err := handler.persistState(task.ID, remotetask.Running, nextState, result); err != nil {
		_ = connection.Close(websocket.StatusInternalError, "file transfer state persistence failed")
		return nil
	}
	encoded, _ := filestream.EncodeResult(result.protocol())
	writeMu.Lock()
	_ = connection.Write(context.Background(), websocket.MessageBinary, encoded)
	writeMu.Unlock()
	_ = connection.Close(websocket.StatusNormalClosure, "file transfer complete")
	return nil
}

func (handler *Handler) upload(
	leaseContext context.Context,
	cancel context.CancelFunc,
	connection *websocket.Conn,
	writeMu *sync.Mutex,
	principal controller.Principal,
	namespace, taskID string,
	spec Spec,
) (Outcome, error) {
	reader, writer := io.Pipe()
	inputResult := make(chan error, 1)
	go func() {
		err := readUpload(leaseContext, connection, writer, writeMu, spec)
		if err != nil {
			cancel()
		}
		inputResult <- err
	}()
	outcome, transferErr := handler.executor.Upload(leaseContext, principal, namespace, taskID, spec, reader)
	_ = reader.CloseWithError(transferErr)
	inputErr := <-inputResult
	if transferErr != nil {
		return outcome, transferErr
	}
	return outcome, inputErr
}

func (handler *Handler) download(
	leaseContext, socketContext context.Context,
	cancel context.CancelFunc,
	connection *websocket.Conn,
	writeMu *sync.Mutex,
	principal controller.Principal,
	namespace, taskID string,
	spec Spec,
) (Outcome, error) {
	go readDownloadControl(socketContext, connection, cancel)
	output := &downloadWriter{
		ctx: leaseContext, connection: connection, mu: writeMu,
		transferred: spec.Offset, maximum: handler.maximumBytes,
	}
	outcome, err := handler.executor.Download(
		leaseContext, principal, namespace, taskID, spec,
		func(metadata DownloadMetadata) error {
			output.total = metadata.Total
			return output.progress()
		},
		output,
	)
	if err != nil {
		return outcome, err
	}
	if outcome.Transferred != output.transferred {
		return outcome, errors.New("file transfer byte count does not match the streamed output")
	}
	return outcome, nil
}

func readUpload(
	ctx context.Context,
	connection *websocket.Conn,
	pipe *io.PipeWriter,
	writeMu *sync.Mutex,
	spec Spec,
) error {
	defer pipe.Close()
	transferred := spec.Offset
	for {
		messageType, encoded, err := connection.Read(ctx)
		if err != nil {
			_ = pipe.CloseWithError(err)
			return err
		}
		if messageType != websocket.MessageBinary {
			err := errors.New("file upload requires binary frames")
			_ = pipe.CloseWithError(err)
			return err
		}
		frame, err := filestream.Decode(encoded)
		if err != nil {
			_ = pipe.CloseWithError(err)
			return err
		}
		switch frame.Type {
		case filestream.Data:
			if transferred > spec.Size || uint64(len(frame.Payload)) > spec.Size-transferred {
				err := errors.New("upload exceeds declared size")
				_ = pipe.CloseWithError(err)
				return err
			}
			if _, err := pipe.Write(frame.Payload); err != nil {
				return err
			}
			transferred += uint64(len(frame.Payload))
			if err := writeProgress(ctx, connection, writeMu, transferred, spec.Size); err != nil {
				return err
			}
		case filestream.Complete:
			if transferred != spec.Size {
				err := errors.New("upload completed before declared size")
				_ = pipe.CloseWithError(err)
				return err
			}
			return nil
		case filestream.Cancel:
			_ = pipe.CloseWithError(context.Canceled)
			return context.Canceled
		default:
			err := errors.New("client sent a server-only file transfer frame")
			_ = pipe.CloseWithError(err)
			return err
		}
	}
}

func readDownloadControl(ctx context.Context, connection *websocket.Conn, cancel context.CancelFunc) {
	for {
		messageType, encoded, err := connection.Read(ctx)
		if err != nil {
			cancel()
			return
		}
		if messageType != websocket.MessageBinary {
			cancel()
			return
		}
		frame, err := filestream.Decode(encoded)
		if err != nil || frame.Type != filestream.Cancel {
			cancel()
			return
		}
		cancel()
		return
	}
}

type downloadWriter struct {
	ctx         context.Context
	connection  *websocket.Conn
	mu          *sync.Mutex
	transferred uint64
	total       uint64
	maximum     uint64
}

func (writer *downloadWriter) Write(value []byte) (int, error) {
	written := 0
	for len(value) > 0 {
		length := min(len(value), filestream.MaximumData)
		chunk := value[:length]
		if writer.transferred > writer.maximum || uint64(length) > writer.maximum-writer.transferred {
			return written, errors.New("download exceeds configured size limit")
		}
		encoded, _ := filestream.Encode(filestream.Frame{Type: filestream.Data, Payload: chunk})
		writer.mu.Lock()
		err := writer.connection.Write(writer.ctx, websocket.MessageBinary, encoded)
		writer.mu.Unlock()
		if err != nil {
			return written, err
		}
		writer.transferred += uint64(length)
		written += length
		value = value[length:]
		if err := writer.progress(); err != nil {
			return written, err
		}
	}
	return written, nil
}

func (writer *downloadWriter) progress() error {
	return writeProgress(writer.ctx, writer.connection, writer.mu, writer.transferred, writer.total)
}

func writeProgress(
	ctx context.Context,
	connection *websocket.Conn,
	writeMu *sync.Mutex,
	transferred, total uint64,
) error {
	encoded, err := filestream.EncodeProgress(filestream.ProgressStatus{Transferred: transferred, Total: total})
	if err != nil {
		return err
	}
	writeMu.Lock()
	err = connection.Write(ctx, websocket.MessageBinary, encoded)
	writeMu.Unlock()
	return err
}

type streamResult struct {
	Transferred uint64 `json:"transferred"`
	Checksum    string `json:"checksum,omitempty"`
	Cancelled   bool   `json:"cancelled,omitempty"`
	Error       string `json:"error,omitempty"`
}

func resultFromOutcome(outcome Outcome, err error, cancelled bool) streamResult {
	result := streamResult{Transferred: outcome.Transferred, Cancelled: cancelled}
	if outcome.HasChecksum {
		result.Checksum = filestream.FormatChecksum(outcome.Checksum)
	}
	if err != nil && !cancelled {
		result.Error = "file transfer failed"
	}
	return result
}

func (result streamResult) protocol() filestream.TransferResult {
	status := filestream.ResultSucceeded
	if result.Cancelled {
		status = filestream.ResultCancelled
	} else if result.Error != "" {
		status = filestream.ResultFailed
	}
	protocol := filestream.TransferResult{
		Status: status, Transferred: result.Transferred, HasChecksum: result.Checksum != "", Error: result.Error,
	}
	if protocol.HasChecksum {
		protocol.Checksum, _ = filestream.ParseChecksum(result.Checksum)
	}
	return protocol
}

func (handler *Handler) persistState(taskID string, expected, next remotetask.State, result streamResult) error {
	encoded, _ := json.Marshal(result)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return handler.storage.Tasks().UpdateState(ctx, taskID, expected, next, encoded, handler.now().UTC())
}
