package taskstream

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/coder/websocket"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

type TaskStore interface {
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

type WatchConfig struct {
	Tasks             TaskStore
	TaskID            string
	Owner             json.RawMessage
	Interval          time.Duration
	Now               func() time.Time
	StopError         error
	InvalidStateError error
	Heartbeat         func(storage.Task, json.RawMessage) json.RawMessage
}

func Watch(ctx context.Context, cancel context.CancelCauseFunc, config WatchConfig) {
	ticker := time.NewTicker(config.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			checkContext, checkCancel := context.WithTimeout(ctx, config.Interval)
			task, err := config.Tasks.GetByID(checkContext, config.TaskID)
			if err == nil {
				switch task.State {
				case remotetask.Starting, remotetask.Running:
					heartbeat := config.Owner
					if config.Heartbeat != nil {
						heartbeat = config.Heartbeat(task, heartbeat)
					}
					err = config.Tasks.UpdateState(
						checkContext, config.TaskID, task.State, task.State, heartbeat, config.Now().UTC(),
					)
					if errors.Is(err, storage.ErrConflict) {
						err = nil
					}
				case remotetask.Stopping, remotetask.Stopped, remotetask.Failed:
					err = config.StopError
				default:
					err = config.InvalidStateError
				}
			}
			checkCancel()
			if err != nil {
				cancel(err)
				return
			}
		}
	}
}

type FinishConfig struct {
	Tasks           TaskStore
	TaskID          string
	Now             func() time.Time
	Cause           error
	CleanupRequired bool
	CleanupComplete bool
	StopErrors      []error
	Result          func(storage.Task, remotetask.State, bool) json.RawMessage
}

func Finish(config FinishConfig) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	task, err := config.Tasks.GetByID(ctx, config.TaskID)
	if err != nil {
		return false
	}
	if task.State == remotetask.Stopped || task.State == remotetask.Failed {
		return true
	}
	if task.State == remotetask.Recovering {
		return false
	}

	next := remotetask.Stopped
	if task.State != remotetask.Stopping && isFailure(config.Cause, config.StopErrors...) {
		next = remotetask.Failed
	}
	cleanupPending := config.CleanupRequired && !config.CleanupComplete
	if cleanupPending {
		next = remotetask.Recovering
	}
	result := config.Result(task, next, cleanupPending)
	return config.Tasks.UpdateState(ctx, config.TaskID, task.State, next, result, config.Now().UTC()) == nil
}

func Failed(err error, runContext context.Context, stopErrors ...error) bool {
	return runContext.Err() == nil && isFailure(err, stopErrors...)
}

func isFailure(err error, stopErrors ...error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		websocket.CloseStatus(err) != -1 {
		return false
	}
	for _, stopError := range stopErrors {
		if errors.Is(err, stopError) {
			return false
		}
	}
	return true
}
