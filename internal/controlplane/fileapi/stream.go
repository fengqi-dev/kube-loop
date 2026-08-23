package fileapi

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/streamlease"
	"github.com/fengqi-dev/kube-loop/internal/protocol/filestream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
	"github.com/fengqi-dev/kube-loop/internal/protocol/websocket"
)

func (handler *Service) stream(
	ctx *echo.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
	taskID string,
) *controlplaneapi.Error {
	request := ctx.Request()
	writer := ctx.Response()
	if _, err := uuid.Parse(taskID); err != nil {
		return notFound()
	}
	task, err := handler.storage.Tasks().GetByID(request.Context(), taskID)
	if err != nil || !owned(task, identity, session) {
		return notFound()
	}
	if task.State != remotetask.Pending {
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeConflict,
			Message: "file transfer Task was already claimed",
		}
	}
	spec, err := handler.specFromTask(task)
	if err != nil {
		return internalError(err)
	}
	if err := handler.storage.Tasks().UpdateState(
		request.Context(), task.ID, remotetask.Pending, remotetask.Starting,
		json.RawMessage(`{}`), handler.now().UTC(),
	); err != nil {
		return storageError(err)
	}
	connection, err := websocket.Accept(
		writer,
		request,
		&websocket.AcceptOptions{
			CompressionMode: websocket.CompressionDisabled,
		},
	)
	if err != nil {
		_ = handler.persistState(
			request.Context(),
			task.ID,
			remotetask.Starting,
			remotetask.Failed,
			streamResult{Error: "WebSocket upgrade failed"},
		)
		return nil
	}
	var readers sync.WaitGroup
	defer func() {
		_ = connection.CloseNow()
		readers.Wait()
	}()
	connection.SetReadLimit(filestream.MaximumData + 1)
	leaseContext, cancel, err := streamlease.Start(
		request.Context(),
		handler.storage,
		identity,
		session,
		streamlease.Config{
			Now: handler.now, CheckInterval: handler.credentialCheckInterval,
			Runtime: streamlease.RuntimeFrom(
				handler.sessions,
			), TaskID: task.ID, HeartbeatTask: true,
			Authorizer: handler.authorizer, Authorization: authorization.Request{
				Operation:    "stream",
				Namespace:    session.Namespace,
				ResourceKind: "file-transfers",
				ResourceName: task.ID,
			},
		},
	)
	if err != nil {
		_ = handler.persistState(
			request.Context(),
			task.ID,
			remotetask.Starting,
			remotetask.Failed,
			streamResult{Error: "authorization lease expired"},
		)
		_ = connection.Close(
			websocket.StatusPolicyViolation,
			"authorization lease expired",
		)
		return nil
	}
	defer cancel()
	if err := handler.persistState(
		request.Context(), task.ID, remotetask.Starting, remotetask.Running, streamResult{},
	); err != nil {
		_ = connection.Close(
			websocket.StatusInternalError,
			"file transfer state persistence failed",
		)
		return nil
	}
	var writeMu sync.Mutex
	var outcome Outcome
	var transferErr error
	if spec.Direction == DirectionUpload {
		outcome, transferErr = handler.upload(
			leaseContext,
			cancel,
			connection,
			&writeMu,
			identity,
			session.Namespace,
			task.ID,
			spec,
		)
	} else {
		outcome, transferErr = handler.download(
			leaseContext, request.Context(), cancel, connection, &writeMu, &readers,
			identity, session.Namespace, task.ID, spec,
		)
	}
	cancelled := leaseContext.Err() != nil ||
		errors.Is(transferErr, context.Canceled)
	result := resultFromOutcome(outcome, transferErr, cancelled)
	nextState := remotetask.Stopped
	if transferErr != nil && !cancelled {
		nextState = remotetask.Failed
	}
	if err := handler.persistState(
		request.Context(), task.ID, remotetask.Running, nextState, result,
	); err != nil {
		_ = connection.Close(
			websocket.StatusInternalError,
			"file transfer state persistence failed",
		)
		return nil
	}
	encoded, _ := filestream.EncodeResult(result.protocol())
	writeContext, cancelWrite := context.WithTimeout(request.Context(), 5*time.Second)
	writeMu.Lock()
	_ = connection.Write(writeContext, websocket.MessageBinary, encoded)
	writeMu.Unlock()
	cancelWrite()
	_ = connection.Close(
		websocket.StatusNormalClosure,
		"file transfer complete",
	)
	return nil
}
