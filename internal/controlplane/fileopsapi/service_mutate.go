package fileopsapi

import (
	"context"
	"encoding/json"

	"github.com/labstack/echo/v5"

	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/taskapi"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

func (handler *Service) mutate(
	ctx *echo.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
	action string,
) *controlplaneapi.Error {
	return taskapi.Creator[Spec, Document]{
		TaskType: TaskType, Storage: handler.storage, Now: handler.now, Errors: apiErrors,
		Normalize: func(spec *Spec) *controlplaneapi.Error {
			// The action comes from the route, not the body, so it is fixed
			// before the spec rules for that action are applied.
			spec.Action = action
			return handler.normalize(spec)
		},
		Prepare:  handler.resolveContainer,
		Document: decodeTask,
		Location: func(session sessionapi.ActiveSession, taskID string) string {
			return controlplane.APIPathPrefix + "/sessions/" + session.ID +
				"/pod-files/operations/" + taskID + "?namespace=" + session.Namespace
		},
		// A file operation completes within the request, so the Task is
		// already terminal by the time it is rendered.
		AfterCreate: handler.execute,
	}.Create(ctx, identity, session)
}

// resolveContainer pins the container the operation will run in before the
// Task is persisted, so a replay cannot land in a different one.
func (handler *Service) resolveContainer(
	ctx context.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
	spec *Spec,
) *controlplaneapi.Error {
	container, err := handler.targets.ResolveContainer(
		ctx, identity, session.Namespace, spec.Pod, spec.Container,
	)
	if err != nil {
		return targetError(err)
	}
	spec.Container = container
	return nil
}

// execute performs the operation and records its outcome, so the response
// already carries the result rather than a Task the client must poll.
func (handler *Service) execute(
	ctx context.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
	task storage.Task,
	spec Spec,
) (storage.Task, error) {
	if err := handler.storage.Tasks().UpdateState(
		ctx, task.ID, remotetask.Pending, remotetask.Running,
		json.RawMessage(`{}`), handler.now().UTC(),
	); err != nil {
		return storage.Task{}, err
	}
	next := remotetask.Stopped
	result := Result{Completed: true}
	if err := handler.operator.Mutate(ctx, identity, session.Namespace, spec); err != nil {
		next = remotetask.Failed
		result = Result{Error: "remote file operation failed"}
	}
	encoded, _ := json.Marshal(result)
	if err := handler.storage.Tasks().UpdateState(
		ctx, task.ID, remotetask.Running, next, encoded, handler.now().UTC(),
	); err != nil {
		return storage.Task{}, err
	}
	return handler.storage.Tasks().GetByID(ctx, task.ID)
}
