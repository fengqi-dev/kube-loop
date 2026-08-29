package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

type activationStorage struct {
	Storage

	tasks storage.TaskRepository
}

func (store activationStorage) Tasks() storage.TaskRepository { return store.tasks }

type activationTasks struct {
	storage.TaskRepository

	task storage.Task
}

func (tasks *activationTasks) UpdateState(
	_ context.Context,
	id string,
	expected, next remotetask.State,
	result json.RawMessage,
	updatedAt time.Time,
) error {
	if id != tasks.task.ID || tasks.task.State != expected {
		return storage.ErrConflict
	}
	tasks.task.State = next
	tasks.task.Result = result
	tasks.task.UpdatedAt = updatedAt
	return nil
}

type failingBindings struct {
	managed          bool
	activateErr      error
	deletedNamespace string
	deletedTaskID    string
	deleteValue      any
}

type activationCleanupContextKey struct{}

func (bindings *failingBindings) Activate(
	context.Context,
	sessionapi.ActiveSession,
	string,
	Spec,
) (bool, error) {
	return bindings.managed, bindings.activateErr
}

func (bindings *failingBindings) Delete(ctx context.Context, namespace, taskID string) error {
	bindings.deletedNamespace = namespace
	bindings.deletedTaskID = taskID
	bindings.deleteValue = ctx.Value(activationCleanupContextKey{})
	return nil
}

func (bindings *failingBindings) Stop(ctx context.Context, namespace, taskID string) error {
	return bindings.Delete(ctx, namespace, taskID)
}

func TestActivateCleansManagedBindingAndMarksTaskFailed(t *testing.T) {
	now := time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC)
	specJSON, err := json.Marshal(Spec{
		Kind: "service", Name: "api", Protocol: "tcp", RemotePort: 8443,
	})
	if err != nil {
		t.Fatal(err)
	}
	task := storage.Task{
		ID: uuid.NewString(), State: remotetask.Pending, Spec: specJSON,
		Result: json.RawMessage(`{"host":"10.96.0.20","port":8443}`),
	}
	tasks := &activationTasks{task: task}
	bindings := &failingBindings{managed: true, activateErr: errors.New("binding failed")}
	service := &Service{
		storage: activationStorage{tasks: tasks}, bindings: bindings, now: func() time.Time { return now },
	}
	session := sessionapi.ActiveSession{ID: uuid.NewString(), Namespace: "development"}

	activateContext := context.WithValue(context.Background(), activationCleanupContextKey{}, "activation")
	apiError := service.activate(activateContext, session, &task)
	if apiError == nil || apiError.Code != controlplaneapi.CodeUnavailable {
		t.Fatalf("activate error = %#v", apiError)
	}
	if task.State != remotetask.Failed || tasks.task.State != remotetask.Failed ||
		!task.UpdatedAt.Equal(now) || !tasks.task.UpdatedAt.Equal(now) {
		t.Fatalf("failed task = %#v persisted = %#v", task, tasks.task)
	}
	if bindings.deletedNamespace != session.Namespace || bindings.deletedTaskID != task.ID {
		t.Fatalf(
			"cleanup = namespace %q task %q",
			bindings.deletedNamespace,
			bindings.deletedTaskID,
		)
	}
	if bindings.deleteValue != "activation" {
		t.Fatalf("cleanup context value = %v", bindings.deleteValue)
	}
}
