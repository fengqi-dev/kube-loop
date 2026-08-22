package fileopsapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	controlplanemiddleware "github.com/fengqi-dev/kube-loop/internal/controlplane/middleware"
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
	request := ctx.Request()
	spec := Spec{}
	if err := ctx.Bind(&spec); err != nil {
		return controlplanemiddleware.BindingError(err)
	}
	spec.Action = action
	if apiError := handler.normalize(&spec); apiError != nil {
		return apiError
	}
	key, apiError := taskapi.IdempotencyKey(request)
	if apiError != nil {
		return apiError
	}
	requestHash, err := taskapi.RequestHash(session.ID, session.Namespace, spec)
	if err != nil {
		return internalError(err)
	}
	scope := taskapi.Scope(TaskType, identity.Subject)
	task, replayed, apiError := handler.replay(
		request.Context(), scope, key, requestHash, identity, session,
	)
	if apiError != nil {
		return apiError
	} else if replayed {
		document, err := decodeTask(task, session.Namespace)
		if err != nil {
			return internalError(err)
		}
		ctx.Response().Header().Set("Idempotent-Replayed", "true")
		writeJSON(ctx, http.StatusOK, document)
		return nil
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
	specJSON, _ := json.Marshal(spec)
	now, expiresAt := handler.now().UTC(), session.ExpiresAt.UTC()
	task = storage.Task{
		ID:             uuid.NewString(),
		IdentityID:     identity.Subject,
		SessionID:      session.ID,
		Type:           TaskType,
		State:          remotetask.Pending,
		Spec:           specJSON,
		IdempotencyKey: key,
		CreatedAt:      now,
		UpdatedAt:      now,
		ExpiresAt:      &expiresAt,
	}
	created := false
	err = handler.storage.WithinTransaction(
		request.Context(),
		func(repositories storage.Repositories) error {
			record, reserved, reserveErr := repositories.Idempotency().
				Reserve(request.Context(), storage.IdempotencyRecord{
					Scope: scope, Key: key, RequestHash: requestHash, ResourceType: TaskType, ResourceID: task.ID,
					CreatedAt: now, ExpiresAt: expiresAt,
				})
			if reserveErr != nil {
				return reserveErr
			}
			if !reserved {
				existing, loadErr := repositories.Tasks().
					GetByID(request.Context(), record.ResourceID)
				if loadErr != nil || !owned(existing, identity, session) {
					return storage.ErrNotFound
				}
				task = existing
				return nil
			}
			if createErr := repositories.Tasks().Create(request.Context(), task); createErr != nil {
				return createErr
			}
			created = true
			return nil
		},
	)
	if err != nil {
		return storageError(err)
	}
	if created {
		task, err = handler.execute(
			request.Context(),
			identity,
			session.Namespace,
			task,
			spec,
		)
		if err != nil {
			return internalError(err)
		}
	} else {
		ctx.Response().Header().Set("Idempotent-Replayed", "true")
	}
	document, err := decodeTask(task, session.Namespace)
	if err != nil {
		return internalError(err)
	}
	location := fmt.Sprintf(
		"%s/sessions/%s/pod-files/operations/%s?namespace=%s",
		controlplane.APIPathPrefix, session.ID, task.ID, session.Namespace,
	)
	ctx.Response().Header().Set("Location", location)
	writeJSON(
		ctx,
		map[bool]int{true: http.StatusCreated, false: http.StatusOK}[created],
		document,
	)
	return nil
}

func (handler *Service) execute(
	ctx context.Context,
	identity controlplaneapi.Identity,
	namespace string,
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
	if err := handler.operator.Mutate(ctx, identity, namespace, spec); err != nil {
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
