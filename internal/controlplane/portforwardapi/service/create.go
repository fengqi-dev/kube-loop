package service

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/taskapi"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

type CreateResult struct {
	PortForward PortForward
	Created     bool
	Replayed    bool
}

func (service *Service) Create(
	ctx context.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
	spec Spec,
	idempotencyKey string,
) (CreateResult, *controlplaneapi.Error) {
	if apiError := normalizeSpec(&spec); apiError != nil {
		return CreateResult{}, apiError
	}
	requestHash, err := taskapi.RequestHash(session.ID, session.Namespace, spec)
	if err != nil {
		return CreateResult{}, internalError(err)
	}
	scope := taskapi.Scope(TaskType, identity.Subject)
	if record, getErr := service.storage.Idempotency().Get(ctx, scope, idempotencyKey); getErr == nil {
		if record.RequestHash != requestHash {
			return CreateResult{}, mapStorageError(
				storage.ErrIdempotencyMismatch,
			)
		}
		if record.ResourceType != TaskType {
			return CreateResult{}, mapStorageError(storage.ErrConflict)
		}
		existing, taskErr := service.storage.Tasks().
			GetByID(ctx, record.ResourceID)
		if taskErr != nil {
			return CreateResult{}, mapStorageError(taskErr)
		}
		if !owned(existing, identity, session) {
			return CreateResult{}, notFound()
		}
		if apiError := service.activate(ctx, session, &existing); apiError != nil {
			return CreateResult{}, apiError
		}
		result, decodeErr := decodeTask(existing, session.Namespace)
		if decodeErr != nil {
			return CreateResult{}, internalError(decodeErr)
		}
		return CreateResult{PortForward: result, Replayed: true}, nil
	} else if !errors.Is(getErr, storage.ErrNotFound) {
		return CreateResult{}, mapStorageError(getErr)
	}
	target, err := service.resolver.Resolve(
		ctx,
		identity,
		session.Namespace,
		spec,
	)
	if err != nil {
		return CreateResult{}, &controlplaneapi.Error{
			Code:    controlplaneapi.CodeUnavailable,
			Message: "Kubernetes Port Forward target resolution failed",
			Cause:   err,
		}
	}
	if err := validateTarget(target); err != nil {
		return CreateResult{}, &controlplaneapi.Error{
			Code:    controlplaneapi.CodeInternal,
			Message: "Port Forward resolver returned an invalid target",
			Cause:   err,
		}
	}
	now := service.now().UTC()
	specJSON, _ := json.Marshal(spec)
	targetJSON, _ := json.Marshal(target)
	expiresAt := session.ExpiresAt.UTC()
	task := storage.Task{
		ID: uuid.NewString(), IdentityID: identity.Subject, SessionID: session.ID,
		Type: TaskType, State: remotetask.Pending, Spec: specJSON, Result: targetJSON,
		IdempotencyKey: idempotencyKey, CreatedAt: now, UpdatedAt: now, ExpiresAt: &expiresAt,
	}
	portForward := portForwardFromTask(task, session.Namespace)
	responseJSON, _ := json.Marshal(portForward)
	created := false
	err = service.storage.WithinTransaction(
		ctx,
		func(repositories storage.Repositories) error {
			record, reserved, reserveErr := repositories.Idempotency().
				Reserve(ctx, storage.IdempotencyRecord{
					Scope: scope, Key: idempotencyKey, RequestHash: requestHash, ResourceType: TaskType,
					ResourceID: task.ID, Response: responseJSON, CreatedAt: now, ExpiresAt: expiresAt,
				})
			if reserveErr != nil {
				return reserveErr
			}
			if !reserved {
				if record.ResourceType != TaskType {
					return storage.ErrConflict
				}
				existing, getErr := repositories.Tasks().
					GetByID(ctx, record.ResourceID)
				if getErr != nil {
					return getErr
				}
				if !owned(existing, identity, session) {
					return storage.ErrNotFound
				}
				task = existing
				return nil
			}
			if createErr := repositories.Tasks().Create(ctx, task); createErr != nil {
				return createErr
			}
			created = true
			return nil
		},
	)
	if err != nil {
		return CreateResult{}, mapStorageError(err)
	}
	if apiError := service.activate(ctx, session, &task); apiError != nil {
		return CreateResult{}, apiError
	}
	portForward, err = decodeTask(task, session.Namespace)
	if err != nil {
		return CreateResult{}, internalError(err)
	}
	return CreateResult{
		PortForward: portForward,
		Created:     created,
		Replayed:    !created,
	}, nil
}
