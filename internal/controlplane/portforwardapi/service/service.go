package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/taskapi"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

const TaskType = "port-forward"

type Storage interface {
	storage.Repositories
	storage.TransactionManager
}

type Resolver interface {
	Resolve(
		context.Context,
		controlplaneapi.Identity,
		string,
		Spec,
	) (Target, error)
}

type BindingManager interface {
	Activate(
		context.Context,
		sessionapi.ActiveSession,
		string,
		Spec,
	) (bool, error)
	Delete(context.Context, string, string) error
}

type Config struct {
	Now func() time.Time
}

type CreateResult struct {
	PortForward PortForward
	Created     bool
	Replayed    bool
}

type Service struct {
	storage  Storage
	resolver Resolver
	bindings BindingManager
	now      func() time.Time
}

func New(
	storageBackend Storage,
	resolver Resolver,
	bindings BindingManager,
	config Config,
) (*Service, error) {
	if storageBackend == nil || resolver == nil || bindings == nil {
		return nil, errors.New(
			"port forward storage, target resolver and TrafficBinding manager are required",
		)
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Service{
		storage:  storageBackend,
		resolver: resolver,
		bindings: bindings,
		now:      config.Now,
	}, nil
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

func (service *Service) List(
	ctx context.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
) ([]PortForward, *controlplaneapi.Error) {
	tasks, err := service.storage.Tasks().ListBySession(ctx, session.ID, 1000)
	if err != nil {
		return nil, mapStorageError(err)
	}
	items := make([]PortForward, 0, len(tasks))
	for _, task := range tasks {
		if task.Type != TaskType || task.IdentityID != identity.Subject {
			continue
		}
		portForward, decodeErr := decodeTask(task, session.Namespace)
		if decodeErr != nil {
			return nil, internalError(decodeErr)
		}
		items = append(items, portForward)
	}
	return items, nil
}

func (service *Service) Stop(
	ctx context.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
	taskID string,
) (PortForward, *controlplaneapi.Error) {
	if _, err := uuid.Parse(taskID); err != nil {
		return PortForward{}, notFound()
	}
	task, err := service.storage.Tasks().GetByID(ctx, taskID)
	if err != nil || !owned(task, identity, session) {
		if err != nil && !errors.Is(err, storage.ErrNotFound) {
			return PortForward{}, mapStorageError(err)
		}
		return PortForward{}, notFound()
	}
	if err := service.bindings.Delete(ctx, session.Namespace, task.ID); err != nil {
		return PortForward{}, &controlplaneapi.Error{
			Code:    controlplaneapi.CodeUnavailable,
			Message: "Port Forward cleanup is pending",
			Cause:   err,
		}
	}
	if !task.State.Terminal() {
		if err := service.storage.Tasks().UpdateState(
			ctx, task.ID, task.State, remotetask.Stopped, task.Result, service.now().UTC(),
		); err != nil {
			return PortForward{}, mapStorageError(err)
		}
		task, err = service.storage.Tasks().GetByID(ctx, task.ID)
		if err != nil {
			return PortForward{}, mapStorageError(err)
		}
	}
	portForward, err := decodeTask(task, session.Namespace)
	if err != nil {
		return PortForward{}, internalError(err)
	}
	return portForward, nil
}
