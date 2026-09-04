package fileapi

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
		Normalize: handler.normalizeSpec,
		Prepare:   handler.resolveTarget,
		Document:  handler.decodeTask,
		Location: func(session sessionapi.ActiveSession, taskID string) string {
			return controlplane.APIPathPrefix + "/sessions/" + session.ID +
				"/file-transfers/" + taskID + "/stream?namespace=" + session.Namespace
		},
		RecordResponse: true,
	}.Create(ctx, identity, session)
}

// resolveTarget pins the container the transfer will run against and, for a
// resumed upload, the offset the Control Plane considers authoritative. Both
// are settled before the Task is persisted, so a replay resumes from the same
// place rather than re-negotiating.
func (handler *Service) resolveTarget(
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
	if spec.ResumeID == "" {
		return nil
	}
	spec.Offset, err = handler.executor.UploadOffset(ctx, identity, session.Namespace, *spec)
	if err != nil {
		return targetError(err)
	}
	if spec.Offset > spec.Size {
		return invalid("resumeId", "remote partial upload exceeds the declared size")
	}
	return nil
}
