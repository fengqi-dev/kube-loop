package execapi

import (
	"context"

	"github.com/labstack/echo/v5"

	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/taskapi"
)

func (handler *Service) create(
	ctx *echo.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
) *controlplaneapi.Error {
	return taskapi.Creator[Spec, Document]{
		TaskType: TaskType, Storage: handler.storage, Now: handler.now, Errors: apiErrors,
		Normalize: normalizeSpec,
		Prepare:   handler.validateTarget,
		Document:  decodeTask,
		Location: func(session sessionapi.ActiveSession, taskID string) string {
			return controlplane.APIPathPrefix + "/sessions/" + session.ID +
				"/exec/" + taskID + "/stream?namespace=" + session.Namespace
		},
		RecordResponse: true,
	}.Create(ctx, identity, session)
}

// validateTarget rejects a Pod exec whose target the caller cannot reach before
// the Task is persisted.
func (handler *Service) validateTarget(
	ctx context.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
	spec *Spec,
) *controlplaneapi.Error {
	if err := handler.executor.Validate(ctx, identity, session.Namespace, *spec); err != nil {
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeInvalidArgument,
			Message: "Pod exec target is unavailable",
			Cause:   err,
		}
	}
	return nil
}
