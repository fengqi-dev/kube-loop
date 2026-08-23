package exchangeapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficmodel"
)

const TaskType = "exchange"

type Storage interface {
	storage.Repositories
	storage.TransactionManager
}

type SessionValidator interface {
	RequireActive(
		context.Context,
		controlplaneapi.Identity,
		string,
		string,
	) (sessionapi.ActiveSession, *controlplaneapi.Error)
}

type ServiceResolver interface {
	ResolveService(
		context.Context,
		controlplaneapi.Identity,
		string,
		string,
		[]trafficmodel.Port,
	) (trafficmodel.ResolvedService, error)
}

type Service struct {
	storage   Storage
	sessions  SessionValidator
	services  ServiceResolver
	resources ResourceMutator
	now       func() time.Time
	config    Config
}

func New(
	storageBackend Storage,
	sessions SessionValidator,
	services ServiceResolver,
	resources ResourceMutator,
	config Config,
) (*Service, error) {
	if storageBackend == nil || sessions == nil || services == nil ||
		resources == nil {
		return nil, errors.New(
			"exchange storage, Session validator, Service resolver and resource mutator are required",
		)
	}
	if err := config.normalize(); err != nil {
		return nil, err
	}
	return &Service{
		storage: storageBackend, sessions: sessions, services: services, resources: resources,
		now: config.Now, config: config,
	}, nil
}

func (handler *Service) get(
	ctx *echo.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
	taskID string,
) *controlplaneapi.Error {
	request := ctx.Request()
	task, apiError := handler.ownedTask(
		request.Context(),
		identity,
		session,
		taskID,
	)
	if apiError != nil {
		return apiError
	}
	document, err := decodeTask(task, session.Namespace)
	if err != nil {
		return internalError(err)
	}
	writeJSON(ctx, http.StatusOK, document)
	return nil
}

func (handler *Service) stop(
	ctx *echo.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
	taskID string,
) *controlplaneapi.Error {
	request := ctx.Request()
	task, apiError := handler.ownedTask(
		request.Context(),
		identity,
		session,
		taskID,
	)
	if apiError != nil {
		return apiError
	}
	next := task.State
	switch task.State {
	case remotetask.Pending:
		next = remotetask.Stopped
	case remotetask.Starting, remotetask.Running, remotetask.Recovering:
		next = remotetask.Stopping
	case remotetask.Stopping, remotetask.Stopped, remotetask.Failed:
	default:
		return internalError(
			fmt.Errorf("stored Exchange Task has invalid state %q", task.State),
		)
	}
	if next != task.State {
		var owner ownerResult
		_ = json.Unmarshal(task.Result, &owner)
		owner.StopRequested = true
		result, _ := json.Marshal(owner)
		now := handler.now().UTC()
		if err := handler.storage.Tasks().
			UpdateState(request.Context(), task.ID, task.State, next, result, now); err != nil {
			return storageError(err)
		}
		task.State, task.Result, task.UpdatedAt = next, result, now
	}
	document, err := decodeTask(task, session.Namespace)
	if err != nil {
		return internalError(err)
	}
	writeJSON(
		ctx,
		map[bool]int{true: http.StatusAccepted, false: http.StatusOK}[next == remotetask.Stopping],
		document,
	)
	return nil
}

func (handler *Service) ownedTask(
	ctx context.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
	taskID string,
) (storage.Task, *controlplaneapi.Error) {
	if _, err := uuid.Parse(taskID); err != nil {
		return storage.Task{}, notFound()
	}
	task, err := handler.storage.Tasks().GetByID(ctx, taskID)
	if err != nil || !owned(task, identity, session) {
		return storage.Task{}, notFound()
	}
	return task, nil
}
