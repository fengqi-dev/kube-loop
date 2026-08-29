package service

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

func (service *Service) List(
	ctx context.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
) ([]PortForward, *controlplaneapi.Error) {
	tasks, err := service.storage.Tasks().ListBySession(ctx, session.ID, 1000)
	if err != nil {
		return nil, mapStorageError(err)
	}
	items := make([]PortForward, 0, len(tasks))
	for _, task := range tasks {
		if task.Type != TaskType || task.IdentityID != identity.Subject {
			continue
		}
		portForward, decodeErr := decodeTask(task, session.Namespace)
		if decodeErr != nil {
			return nil, internalError(decodeErr)
		}
		items = append(items, portForward)
	}
	return items, nil
}

func (service *Service) Pause(
	ctx context.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
	taskID string,
) (PortForward, *controlplaneapi.Error) {
	if _, err := uuid.Parse(taskID); err != nil {
		return PortForward{}, notFound()
	}
	task, err := service.storage.Tasks().GetByID(ctx, taskID)
	if err != nil || !owned(task, identity, session) {
		if err != nil && !errors.Is(err, storage.ErrNotFound) {
			return PortForward{}, mapStorageError(err)
		}
		return PortForward{}, notFound()
	}
	if err := pausePortForwardBinding(ctx, service.bindings, session.Namespace, task.ID); err != nil {
		return PortForward{}, &controlplaneapi.Error{
			Code:    controlplaneapi.CodeUnavailable,
			Message: "Port Forward cleanup is pending",
			Cause:   err,
		}
	}
	if !task.State.Terminal() {
		if err := service.storage.Tasks().UpdateState(
			ctx, task.ID, task.State, remotetask.Stopped, task.Result, service.now().UTC(),
		); err != nil {
			return PortForward{}, mapStorageError(err)
		}
		task, err = service.storage.Tasks().GetByID(ctx, task.ID)
		if err != nil {
			return PortForward{}, mapStorageError(err)
		}
	}
	portForward, err := decodeTask(task, session.Namespace)
	if err != nil {
		return PortForward{}, internalError(err)
	}
	return portForward, nil
}

func pausePortForwardBinding(
	ctx context.Context, bindings BindingManager, namespace, taskID string,
) error {
	pauser, ok := bindings.(interface {
		Pause(context.Context, string, string) error
	})
	if ok {
		return pauser.Pause(ctx, namespace, taskID)
	}
	return bindings.Stop(ctx, namespace, taskID)
}

func (service *Service) Resume(
	ctx context.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
	taskID string,
) (PortForward, *controlplaneapi.Error) {
	task, apiError := service.ownedTask(ctx, identity, session, taskID)
	if apiError != nil {
		return PortForward{}, apiError
	}
	if task.State == remotetask.Stopped {
		now := service.now().UTC()
		if err := service.storage.Tasks().UpdateState(
			ctx, task.ID, task.State, remotetask.Pending, task.Result, now,
		); err != nil {
			return PortForward{}, mapStorageError(err)
		}
		task.State, task.UpdatedAt = remotetask.Pending, now
	} else if task.State != remotetask.Pending && task.State != remotetask.Running {
		return PortForward{}, mapStorageError(storage.ErrConflict)
	}
	if apiError := service.activate(ctx, session, &task); apiError != nil {
		return PortForward{}, apiError
	}
	result, err := decodeTask(task, session.Namespace)
	if err != nil {
		return PortForward{}, internalError(err)
	}
	return result, nil
}

func (service *Service) Delete(
	ctx context.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
	taskID string,
) (PortForward, *controlplaneapi.Error) {
	task, apiError := service.ownedTask(ctx, identity, session, taskID)
	if apiError != nil {
		return PortForward{}, apiError
	}
	if err := service.bindings.Delete(ctx, session.Namespace, task.ID); err != nil {
		return PortForward{}, &controlplaneapi.Error{
			Code: controlplaneapi.CodeUnavailable, Message: "Port Forward deletion is pending", Cause: err,
		}
	}
	if task.State != remotetask.Stopped {
		now := service.now().UTC()
		if err := service.storage.Tasks().UpdateState(
			ctx, task.ID, task.State, remotetask.Stopped, task.Result, now,
		); err != nil {
			return PortForward{}, mapStorageError(err)
		}
		task.State, task.UpdatedAt = remotetask.Stopped, now
	}
	result, err := decodeTask(task, session.Namespace)
	if err != nil {
		return PortForward{}, internalError(err)
	}
	return result, nil
}

func (service *Service) ownedTask(
	ctx context.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
	taskID string,
) (storage.Task, *controlplaneapi.Error) {
	if _, err := uuid.Parse(taskID); err != nil {
		return storage.Task{}, notFound()
	}
	task, err := service.storage.Tasks().GetByID(ctx, taskID)
	if err != nil || !owned(task, identity, session) {
		if err != nil && !errors.Is(err, storage.ErrNotFound) {
			return storage.Task{}, mapStorageError(err)
		}
		return storage.Task{}, notFound()
	}
	return task, nil
}
