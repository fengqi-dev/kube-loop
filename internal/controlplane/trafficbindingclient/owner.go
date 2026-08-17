package trafficbindingclient

import (
	"context"
	"errors"
	"fmt"
	"math"

	controlplanestorage "github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

type Owner struct {
	SessionID         string
	TaskID            string
	SessionGeneration int64
}

func OwnerForTask(
	ctx context.Context,
	repositories controlplanestorage.Repositories,
	taskID, expectedType, expectedNamespace string,
) (Owner, error) {
	if repositories == nil {
		return Owner{}, errors.New("TrafficBinding storage is required")
	}
	task, err := repositories.Tasks().GetByID(ctx, taskID)
	if err != nil {
		return Owner{}, fmt.Errorf("read TrafficBinding Task: %w", err)
	}
	if task.Type != expectedType {
		return Owner{}, fmt.Errorf("Task %s has type %q instead of %q", task.ID, task.Type, expectedType)
	}
	session, err := repositories.Sessions().GetByID(ctx, task.SessionID)
	if err != nil {
		return Owner{}, fmt.Errorf("read TrafficBinding Session: %w", err)
	}
	if session.ID != task.SessionID || session.Namespace != expectedNamespace {
		return Owner{}, errors.New("TrafficBinding Task and Session ownership do not match")
	}
	if session.Generation == 0 || session.Generation > math.MaxInt64 {
		return Owner{}, errors.New("TrafficBinding Session generation is invalid")
	}
	return Owner{SessionID: session.ID, TaskID: task.ID, SessionGeneration: int64(session.Generation)}, nil
}
