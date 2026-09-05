package fileopsapi

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	controlplanemiddleware "github.com/fengqi-dev/kube-loop/internal/controlplane/middleware"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/taskapi"
)

func (handler *Service) list(
	ctx *echo.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
) *controlplaneapi.Error {
	request := ctx.Request()
	spec := Spec{}
	if err := ctx.Bind(&spec); err != nil {
		return controlplanemiddleware.BindingError(err)
	}
	spec.Action = "list"
	if apiError := handler.normalize(&spec); apiError != nil {
		return apiError
	}
	container, err := handler.targets.ResolveContainer(
		request.Context(),
		identity,
		session.Namespace,
		spec.Pod,
		spec.Container,
	)
	if err != nil {
		return targetError(err)
	}
	spec.Container = container
	items, err := handler.operator.List(
		request.Context(),
		identity,
		session.Namespace,
		spec,
	)
	if err != nil {
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeUnavailable,
			Message: "remote directory could not be read",
			Cause:   err,
		}
	}
	taskapi.WriteJSON(ctx, http.StatusOK, ListDocument{
		SessionID: session.ID, Namespace: session.Namespace,
		Pod: spec.Pod, Container: container, Path: spec.Path, Items: items,
	})
	return nil
}
func (handler *Service) get(
	ctx *echo.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
	taskID string,
) *controlplaneapi.Error {
	request := ctx.Request()
	if _, err := uuid.Parse(taskID); err != nil {
		return controlplaneapi.NotFound()
	}
	task, err := handler.storage.Tasks().GetByID(request.Context(), taskID)
	if err != nil || !taskapi.Owned(task, TaskType, identity, session) {
		return controlplaneapi.NotFound()
	}
	document, err := decodeTask(task, session.Namespace)
	if err != nil {
		return apiErrors.Internal(err)
	}
	taskapi.WriteJSON(ctx, http.StatusOK, document)
	return nil
}
