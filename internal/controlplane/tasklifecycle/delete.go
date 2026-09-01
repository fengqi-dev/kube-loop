package tasklifecycle

import (
	"context"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

// Delete distinguishes an explicitly deleted TrafficBinding from a retained,
// stopped binding that must be synchronized on the next desktop startup.
func Delete(
	ctx context.Context,
	tasks taskRepository,
	taskID string,
	now func() time.Time,
) (storage.Task, error) {
	current, err := tasks.GetByID(ctx, taskID)
	if err != nil {
		return storage.Task{}, err
	}
	if current.State == remotetask.Deleted {
		return current, nil
	}
	task, err := Stop(ctx, tasks, taskID, now)
	if err != nil {
		return storage.Task{}, err
	}
	updatedAt := now().UTC()
	if err := tasks.UpdateState(
		ctx, task.ID, remotetask.Stopped, remotetask.Deleted, task.Result, updatedAt,
	); err != nil {
		return storage.Task{}, err
	}
	task.State, task.UpdatedAt = remotetask.Deleted, updatedAt
	return task, nil
}
