package fileapi

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v5"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/streamlease"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/taskapi"
	"github.com/fengqi-dev/kube-loop/internal/protocol/filestream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
	"github.com/fengqi-dev/kube-loop/internal/transport/websocketio"
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
	if err != nil || !taskapi.Owned(task, TaskType, identity, session) {
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
	connection, err := websocketio.Upgrade(writer, request)
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
		_ = connection.Close()
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
		_ = websocketio.Close(
			connection, websocket.ClosePolicyViolation,
			"authorization lease expired",
		)
		return nil
	}
	defer cancel()
	if err := handler.persistState(
		request.Context(), task.ID, remotetask.Starting, remotetask.Running, streamResult{},
	); err != nil {
		_ = websocketio.Close(
			connection, websocket.CloseInternalServerErr,
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
	cancelled := transferWasCancelled(
		leaseContext,
		transferErr,
		handler.credentialCheckInterval,
	)
	result := resultFromOutcome(outcome, transferErr, cancelled)
	nextState := remotetask.Stopped
	if transferErr != nil && !cancelled {
		nextState = remotetask.Failed
		// The client is told only that the transfer failed, because the
		// container's own message may describe paths it must not learn about.
		// Record the real cause here or it is lost entirely.
		handler.logger.ErrorContext(
			request.Context(),
			"file transfer failed",
			"error", transferErr,
			"task", task.ID,
			"direction", spec.Direction,
			"kind", spec.Kind,
			"offset", spec.Offset,
			"size", spec.Size,
			"transferred", outcome.Transferred,
		)
	}
	if err := handler.persistState(
		request.Context(), task.ID, remotetask.Running, nextState, result,
	); err != nil {
		_ = websocketio.Close(
			connection, websocket.CloseInternalServerErr,
			"file transfer state persistence failed",
		)
		return nil
	}
	encoded, _ := filestream.EncodeResult(result.protocol())
	writeContext, cancelWrite := context.WithTimeout(request.Context(), 5*time.Second)
	writeMu.Lock()
	_ = websocketio.Write(writeContext, connection, websocket.BinaryMessage, encoded)
	writeMu.Unlock()
	cancelWrite()
	_ = websocketio.Close(
		connection, websocket.CloseNormalClosure,
		"file transfer complete",
	)
	return nil
}

func transferWasCancelled(
	leaseContext context.Context,
	transferErr error,
	checkInterval time.Duration,
) bool {
	if leaseContext.Err() != nil || errors.Is(transferErr, context.Canceled) {
		return true
	}
	if transferErr == nil {
		return false
	}

	// A revoked client can close the upload before the periodic lease check
	// observes the revocation. Give that check one full interval to distinguish
	// an ended lease from an ordinary executor failure.
	timer := time.NewTimer(checkInterval)
	defer timer.Stop()
	select {
	case <-leaseContext.Done():
		return true
	case <-timer.C:
		return false
	}
}
