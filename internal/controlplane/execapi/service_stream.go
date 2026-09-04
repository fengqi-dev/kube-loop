package execapi

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v5"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/streamlease"
	"github.com/fengqi-dev/kube-loop/internal/protocol/execstream"
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
	if err != nil || !owned(task, identity, session) {
		return notFound()
	}
	if task.State != remotetask.Pending {
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeConflict,
			Message: "Pod exec Task was already claimed",
		}
	}
	spec, err := specFromTask(task)
	if err != nil {
		return internalError(err)
	}
	if err := handler.storage.Tasks().UpdateState(
		request.Context(),
		task.ID,
		remotetask.Pending,
		remotetask.Starting,
		json.RawMessage(`{}`),
		handler.now().UTC(),
	); err != nil {
		return storageError(err)
	}
	connection, err := websocketio.Upgrade(writer, request)
	if err != nil {
		_ = handler.storage.Tasks().UpdateState(
			request.Context(),
			task.ID,
			remotetask.Starting,
			remotetask.Failed,
			json.RawMessage(`{"error":"WebSocket upgrade failed"}`),
			handler.now().UTC(),
		)
		return nil
	}
	var readers sync.WaitGroup
	defer func() {
		_ = connection.Close()
		readers.Wait()
	}()
	connection.SetReadLimit(execstream.MaximumPayload + 1)
	streamContext, cancel, contextErr := streamlease.Start(
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
				Operation: "stream", Namespace: session.Namespace, ResourceKind: "pod-exec", ResourceName: task.ID,
			},
		},
	)
	if contextErr != nil {
		persistContext, persistCancel := context.WithTimeout(
			context.WithoutCancel(request.Context()),
			5*time.Second,
		)
		_ = handler.storage.Tasks().UpdateState(
			persistContext,
			task.ID,
			remotetask.Starting,
			remotetask.Failed,
			json.RawMessage(`{"error":"authorization lease expired"}`),
			handler.now().UTC(),
		)
		persistCancel()
		_ = websocketio.Close(connection, websocket.ClosePolicyViolation, "authorization lease expired")
		return nil
	}
	defer cancel()
	if err := handler.storage.Tasks().UpdateState(
		request.Context(),
		task.ID,
		remotetask.Starting,
		remotetask.Running,
		json.RawMessage(`{}`),
		handler.now().UTC(),
	); err != nil {
		_ = websocketio.Close(connection, websocket.CloseInternalServerErr, "exec state persistence failed")
		return nil
	}
	stdinReader, stdinWriter := io.Pipe()
	defer func() { _ = stdinReader.Close() }()
	sizes := newTerminalSizeQueue()
	defer sizes.Close()
	readers.Go(func() {
		inputErr := readInput(request.Context(), connection, stdinWriter, sizes)
		if inputErr != nil {
			cancel()
		}
	})
	var writeMu sync.Mutex
	stdout := frameWriter{
		ctx: streamContext, connection: connection, frameType: execstream.Stdout, mu: &writeMu,
	}
	streams := Streams{
		Stdin: stdinReader, Stdout: stdout,
		TTY: spec.TTY, TerminalSizeQueue: sizes,
	}
	if !spec.TTY {
		streams.Stderr = frameWriter{
			ctx:        streamContext,
			connection: connection,
			frameType:  execstream.Stderr,
			mu:         &writeMu,
		}
	}
	execErr := handler.executor.Exec(streamContext, identity, session.Namespace, spec, streams)
	cancelled := streamContext.Err() != nil
	_ = stdinWriter.Close()
	exitStatus := statusFromError(execErr, cancelled)
	nextState := remotetask.Stopped
	if execErr != nil && !cancelled {
		nextState = remotetask.Failed
	}
	persistContext, persistCancel := context.WithTimeout(
		context.WithoutCancel(request.Context()),
		5*time.Second,
	)
	persistErr := handler.storage.Tasks().
		UpdateState(persistContext, task.ID, remotetask.Running, nextState, taskResult(exitStatus), handler.now().UTC())
	persistCancel()
	if persistErr != nil {
		cancel()
		_ = websocketio.Close(connection, websocket.CloseInternalServerErr, "exec state persistence failed")
		return nil
	}
	encoded, _ := execstream.EncodeExit(exitStatus)
	writeContext, writeCancel := context.WithTimeout(request.Context(), 5*time.Second)
	writeMu.Lock()
	_ = websocketio.Write(writeContext, connection, websocket.BinaryMessage, encoded)
	writeMu.Unlock()
	writeCancel()
	cancel()
	_ = websocketio.Close(connection, websocket.CloseNormalClosure, "exec complete")
	return nil
}
