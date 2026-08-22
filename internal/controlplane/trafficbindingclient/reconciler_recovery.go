package trafficbindingclient

import (
	"context"
	"errors"
	"fmt"
	"time"

	controlplanestorage "github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

func (reconciler *Reconciler) recoverStaleTasks(
	ctx context.Context,
) (int, error) {
	now := reconciler.now().UTC()
	before := now.Add(-reconciler.staleAfter)
	recovered := 0
	var result error
	for _, taskType := range recoverableTaskTypes {
		tasks, err := reconciler.tasks.ListStaleByTypeStates(
			ctx,
			taskType,
			recoverableStates,
			before,
			reconciler.batchSize,
		)
		if err != nil {
			result = errors.Join(
				result,
				fmt.Errorf("list stale %s Tasks: %w", taskType, err),
			)
			continue
		}
		for _, task := range tasks {
			claimed, err := reconciler.recoverTask(ctx, task, now)
			if claimed {
				recovered++
			}
			result = errors.Join(result, err)
		}
	}
	return recovered, result
}

func (reconciler *Reconciler) recoverTask(
	ctx context.Context,
	task controlplanestorage.Task,
	now time.Time,
) (bool, error) {
	if err := reconciler.tasks.ClaimStale(
		ctx, task.ID, task.State, task.UpdatedAt, remotetask.Recovering, task.Result, now,
	); err != nil {
		if errors.Is(err, controlplanestorage.ErrConflict) ||
			errors.Is(err, controlplanestorage.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	session, err := reconciler.sessions.GetByID(ctx, task.SessionID)
	if err != nil {
		reconciler.deferRecovery(task, now)
		return true, fmt.Errorf(
			"read Session %s for stale Task %s: %w",
			task.SessionID,
			task.ID,
			err,
		)
	}
	cleanupContext, cancel := context.WithTimeout(
		context.WithoutCancel(ctx),
		reconciler.cleanupTimeout,
	)
	err = reconciler.manager.Delete(cleanupContext, session.Namespace, task.ID)
	cancel()
	if err != nil {
		reconciler.deferRecovery(task, reconciler.now().UTC())
		return true, fmt.Errorf(
			"delete TrafficBinding for stale Task %s: %w",
			task.ID,
			err,
		)
	}
	next := remotetask.Failed
	if task.State == remotetask.Stopping {
		next = remotetask.Stopped
	}
	if err := reconciler.tasks.UpdateState(
		ctx, task.ID, remotetask.Recovering, next, task.Result, reconciler.now().UTC(),
	); err != nil {
		if !errors.Is(err, controlplanestorage.ErrConflict) {
			return true, err
		}
		current, getErr := reconciler.tasks.GetByID(ctx, task.ID)
		if getErr != nil {
			return true, getErr
		}
		if current.State != remotetask.Stopping {
			return true, err
		}
		return true, reconciler.tasks.UpdateState(
			ctx,
			task.ID,
			remotetask.Stopping,
			remotetask.Stopped,
			current.Result,
			reconciler.now().UTC(),
		)
	}
	return true, nil
}

func (reconciler *Reconciler) deferRecovery(
	task controlplanestorage.Task,
	now time.Time,
) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = reconciler.tasks.UpdateState(
		ctx,
		task.ID,
		remotetask.Recovering,
		remotetask.Recovering,
		task.Result,
		now,
	)
}
