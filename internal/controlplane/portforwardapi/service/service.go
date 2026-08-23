package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
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
