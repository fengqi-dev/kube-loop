package tasklifecycle

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

type competingTaskRepository struct {
	task        storage.Task
	updates     int
	competeOnce bool
}

func (repository *competingTaskRepository) GetByID(
	context.Context,
	string,
) (storage.Task, error) {
	return repository.task, nil
}

func (repository *competingTaskRepository) UpdateState(
	_ context.Context,
	_ string,
	from, to remotetask.State,
	result json.RawMessage,
	updatedAt time.Time,
) error {
	repository.updates++
	if repository.competeOnce {
		repository.competeOnce = false
		repository.task.State = remotetask.Recovering
		repository.task.Result = json.RawMessage(`{"owner":"latest"}`)
		return storage.ErrConflict
	}
	if repository.task.State != from {
		return storage.ErrConflict
	}
	repository.task.State = to
	repository.task.Result = result
	repository.task.UpdatedAt = updatedAt
	return nil
}

func TestStopReloadsTaskAfterConflict(t *testing.T) {
	now := time.Now().UTC()
	repository := &competingTaskRepository{
		task: storage.Task{
			ID: "task", State: remotetask.Stopping,
			Result: json.RawMessage(`{"owner":"stale"}`),
		},
		competeOnce: true,
	}

	task, err := Stop(t.Context(), repository, repository.task.ID, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if task.State != remotetask.Stopped || repository.updates != 2 ||
		string(task.Result) != `{"owner":"latest"}` {
		t.Fatalf("stopped task=%#v updates=%d", task, repository.updates)
	}
}

func TestStopIsIdempotent(t *testing.T) {
	repository := &competingTaskRepository{
		task: storage.Task{ID: "task", State: remotetask.Stopped},
	}

	task, err := Stop(t.Context(), repository, repository.task.ID, time.Now)
	if err != nil || task.State != remotetask.Stopped || repository.updates != 0 {
		t.Fatalf("stopped task=%#v updates=%d err=%v", task, repository.updates, err)
	}
}
