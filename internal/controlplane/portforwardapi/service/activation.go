package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

func (service *Service) activate(
	ctx context.Context,
	session sessionapi.ActiveSession,
	task *storage.Task,
) *controlplaneapi.Error {
	if task.State != remotetask.Pending {
		return nil
	}
	var spec Spec
	if err := json.Unmarshal(task.Spec, &spec); err != nil {
		return internalError(err)
	}
	managed, err := service.bindings.Activate(ctx, session, task.ID, spec)
	if err != nil {
		if managed {
			cleanupContext, cancel := context.WithTimeout(
				context.Background(),
				30*time.Second,
			)
			_ = service.bindings.Delete(
				cleanupContext,
				session.Namespace,
				task.ID,
			)
			cancel()
		}
		now := service.now().UTC()
		_ = service.storage.Tasks().
			UpdateState(ctx, task.ID, remotetask.Pending, remotetask.Failed, task.Result, now)
		task.State, task.UpdatedAt = remotetask.Failed, now
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeUnavailable,
			Message: "Kubernetes Port Forward binding failed",
			Cause:   err,
		}
	}
	now := service.now().UTC()
	if err := service.storage.Tasks().UpdateState(
		ctx, task.ID, remotetask.Pending, remotetask.Running, task.Result, now,
	); err != nil {
		if errors.Is(err, storage.ErrConflict) {
			current, getErr := service.storage.Tasks().GetByID(ctx, task.ID)
			if getErr == nil && current.State == remotetask.Running {
				*task = current
				return nil
			}
		}
		return mapStorageError(err)
	}
	task.State, task.UpdatedAt = remotetask.Running, now
	return nil
}
