package mirrorapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

func (handler *Service) get(
	ctx *echo.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
	taskID string,
) *controlplaneapi.Error {
	request := ctx.Request()
	task, apiError := handler.ownedTask(
		request.Context(),
		identity,
		session,
		taskID,
	)
	if apiError != nil {
		return apiError
	}
	document, err := decodeTask(task, session.Namespace)
	if err != nil {
		return internalError(err)
	}
	writeJSON(ctx, http.StatusOK, document)
	return nil
}

func (handler *Service) pause(
	ctx *echo.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
	taskID string,
) *controlplaneapi.Error {
	request := ctx.Request()
	task, apiError := handler.ownedTask(
		request.Context(),
		identity,
		session,
		taskID,
	)
	if apiError != nil {
		return apiError
	}
	next := task.State
	switch task.State {
	case remotetask.Pending:
		next = remotetask.Stopped
	case remotetask.Starting, remotetask.Running, remotetask.Recovering:
		next = remotetask.Stopping
	case remotetask.Stopping, remotetask.Stopped, remotetask.Failed:
	default:
		return internalError(
			fmt.Errorf("stored Mirror Task has invalid state %q", task.State),
		)
	}
	if next != task.State {
		var owner ownerResult
		_ = json.Unmarshal(task.Result, &owner)
		owner.StopRequested = true
		result, _ := json.Marshal(owner)
		now := handler.now().UTC()
		if err := handler.storage.Tasks().
			UpdateState(request.Context(), task.ID, task.State, next, result, now); err != nil {
			return storageError(err)
		}
		task.State, task.Result, task.UpdatedAt = next, result, now
	}
	document, err := decodeTask(task, session.Namespace)
	if err != nil {
		return internalError(err)
	}
	writeJSON(
		ctx,
		map[bool]int{true: http.StatusAccepted, false: http.StatusOK}[next == remotetask.Stopping],
		document,
	)
	return nil
}

func (handler *Service) resume(
	ctx *echo.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
	taskID string,
) *controlplaneapi.Error {
	task, apiError := handler.ownedTask(ctx.Request().Context(), identity, session, taskID)
	if apiError != nil {
		return apiError
	}
	if task.State == remotetask.Stopped {
		now := handler.now().UTC()
		if err := handler.storage.Tasks().UpdateState(
			ctx.Request().Context(), task.ID, task.State, remotetask.Pending, nil, now,
		); err != nil {
			return storageError(err)
		}
		task.State, task.Result, task.UpdatedAt = remotetask.Pending, nil, now
	} else if task.State != remotetask.Pending {
		return storageError(storage.ErrConflict)
	}
	document, err := decodeTask(task, session.Namespace)
	if err != nil {
		return internalError(err)
	}
	writeJSON(ctx, http.StatusAccepted, document)
	return nil
}

func (handler *Service) delete(
	ctx *echo.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
	taskID string,
) *controlplaneapi.Error {
	task, apiError := handler.ownedTask(ctx.Request().Context(), identity, session, taskID)
	if apiError != nil {
		return apiError
	}
	if err := deleteMirrorBinding(ctx.Request().Context(), handler.resources, session.Namespace, task.ID); err != nil {
		return internalError(err)
	}
	if task.State != remotetask.Stopped {
		now := handler.now().UTC()
		if err := handler.storage.Tasks().UpdateState(
			ctx.Request().Context(), task.ID, task.State, remotetask.Stopped, task.Result, now,
		); err != nil {
			return storageError(err)
		}
		task.State, task.UpdatedAt = remotetask.Stopped, now
	}
	document, err := decodeTask(task, session.Namespace)
	if err != nil {
		return internalError(err)
	}
	writeJSON(ctx, http.StatusOK, document)
	return nil
}

func deleteMirrorBinding(
	ctx context.Context, resources ResourceMutator, namespace, taskID string,
) error {
	deleter, ok := resources.(interface {
		DeleteBinding(context.Context, string, string) error
	})
	if !ok {
		return errors.New("mirror deletion is unavailable")
	}
	return deleter.DeleteBinding(ctx, namespace, taskID)
}

func (handler *Service) ownedTask(
	ctx context.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
	taskID string,
) (storage.Task, *controlplaneapi.Error) {
	if _, err := uuid.Parse(taskID); err != nil {
		return storage.Task{}, notFound()
	}
	task, err := handler.storage.Tasks().GetByID(ctx, taskID)
	if err != nil || !owned(task, identity, session) {
		return storage.Task{}, notFound()
	}
	return task, nil
}
