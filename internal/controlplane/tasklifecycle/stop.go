package tasklifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

const stopTransitionAttempts = 8

type taskRepository interface {
	GetByID(context.Context, string) (storage.Task, error)
	UpdateState(
		context.Context,
		string,
		remotetask.State,
		remotetask.State,
		json.RawMessage,
		time.Time,
	) error
}

// Stop reloads the Task after competing state changes so cleanup never writes
// a terminal state using a stale pre-cleanup snapshot.
func Stop(
	ctx context.Context,
	tasks taskRepository,
	taskID string,
	now func() time.Time,
) (storage.Task, error) {
	var lastErr error
	for range stopTransitionAttempts {
		task, err := tasks.GetByID(ctx, taskID)
		if err != nil {
			return storage.Task{}, err
		}
		if task.State == remotetask.Stopped {
			return task, nil
		}
		updatedAt := now().UTC()
		err = tasks.UpdateState(
			ctx,
			task.ID,
			task.State,
			remotetask.Stopped,
			task.Result,
			updatedAt,
		)
		if err == nil {
			task.State = remotetask.Stopped
			task.UpdatedAt = updatedAt
			return task, nil
		}
		if !errors.Is(err, storage.ErrConflict) {
			return storage.Task{}, err
		}
		lastErr = err
		if err := ctx.Err(); err != nil {
			return storage.Task{}, err
		}
	}
	return storage.Task{}, lastErr
}
