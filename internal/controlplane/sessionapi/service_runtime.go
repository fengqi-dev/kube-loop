package sessionapi

import (
	"context"
	"errors"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

func (handler *Service) AttachRuntime(
	parent context.Context,
	sessionID, taskID string,
) (context.Context, func(), error) {
	return handler.registry.Attach(parent, sessionID, taskID)
}

func (handler *Service) disconnectRuntime(
	parent context.Context,
	sessionID string,
) *controlplaneapi.Error {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	if err := handler.registry.Disconnect(ctx, sessionID); err != nil {
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeUnavailable,
			Message: "Session runtime cleanup is pending",
			Cause:   err,
		}
	}
	if err := handler.settleOwnedTasks(ctx, sessionID); err != nil {
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeUnavailable,
			Message: "Session Task cleanup is pending",
			Cause:   err,
		}
	}
	return nil
}

func (handler *Service) settleOwnedTasks(ctx context.Context, sessionID string) error {
	tasks, err := handler.storage.Tasks().ListBySession(ctx, sessionID, 1000)
	if err != nil {
		return err
	}
	var result error
	for _, task := range tasks {
		if task.State.Terminal() {
			continue
		}
		resourceBacked := task.Type == "exchange" || task.Type == "mirror" || task.Type == "preview"
		if resourceBacked && task.State == remotetask.Recovering {
			// The feature-specific recovery worker owns this state and its
			// heartbeat; do not postpone its stale-owner boundary on repeated GET.
			continue
		}
		next := remotetask.Failed
		switch {
		case task.State == remotetask.Pending || task.Type == "port-forward":
			next = remotetask.Stopped
		case resourceBacked:
			next = remotetask.Recovering
		case task.State == remotetask.Stopping:
			next = remotetask.Stopped
		}
		if updateErr := handler.storage.Tasks().UpdateState(
			ctx, task.ID, task.State, next, task.Result, handler.now().UTC(),
		); updateErr != nil && !errors.Is(updateErr, storage.ErrConflict) && !errors.Is(updateErr, storage.ErrNotFound) {
			result = errors.Join(result, updateErr)
		}
	}
	return result
}
