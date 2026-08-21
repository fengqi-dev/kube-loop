package mirrorapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	controlplanemiddleware "github.com/fengqi-dev/kube-loop/internal/controlplane/middleware"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/taskapi"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficmodel"
)

const TaskType = "mirror"

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
			"mirror storage, Session validator, Service resolver and resource mutator are required",
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

func (handler *Service) create(
	ctx *echo.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
) *controlplaneapi.Error {
	request := ctx.Request()
	var spec Spec
	if err := ctx.Bind(&spec); err != nil {
		return controlplanemiddleware.BindingError(err)
	}
	if apiError := normalizeRequest(&spec); apiError != nil {
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
	if record, err := handler.storage.Idempotency().Get(request.Context(), scope, key); err == nil {
		if record.RequestHash != requestHash {
			return storageError(storage.ErrIdempotencyMismatch)
		}
		task, err := handler.storage.Tasks().
			GetByID(request.Context(), record.ResourceID)
		if err != nil || !owned(task, identity, session) {
			return notFound()
		}
		document, err := decodeTask(task, session.Namespace)
		if err != nil {
			return internalError(err)
		}
		ctx.Response().Header().Set("Idempotent-Replayed", "true")
		writeJSON(ctx, http.StatusOK, document)
		return nil
	} else if !errors.Is(err, storage.ErrNotFound) {
		return storageError(err)
	}
	service, err := handler.services.ResolveService(
		request.Context(),
		identity,
		session.Namespace,
		spec.Service,
		spec.Ports,
	)
	if err != nil {
		return targetError(err)
	}
	canonical := storedSpec{
		Service:   service.Name,
		ClusterIP: service.ClusterIP,
		Ports:     append([]trafficmodel.Port(nil), service.Ports...),
	}
	specJSON, _ := json.Marshal(canonical)
	now := handler.now().UTC()
	expiresAt := session.ExpiresAt.UTC()
	task := storage.Task{
		ID: uuid.NewString(), IdentityID: identity.Subject, SessionID: session.ID,
		Type: TaskType, State: remotetask.Pending, Spec: specJSON, IdempotencyKey: key,
		CreatedAt: now, UpdatedAt: now, ExpiresAt: &expiresAt,
	}
	document := documentFrom(task, session.Namespace, canonical)
	response, _ := json.Marshal(document)
	created := false
	err = handler.storage.WithinTransaction(
		request.Context(),
		func(repositories storage.Repositories) error {
			record, reserved, err := repositories.Idempotency().
				Reserve(request.Context(), storage.IdempotencyRecord{
					Scope: scope, Key: key, RequestHash: requestHash, ResourceType: TaskType,
					ResourceID: task.ID, Response: response, CreatedAt: now, ExpiresAt: expiresAt,
				})
			if err != nil {
				return err
			}
			if !reserved {
				existing, err := repositories.Tasks().
					GetByID(request.Context(), record.ResourceID)
				if err != nil || !owned(existing, identity, session) {
					return storage.ErrNotFound
				}
				task = existing
				return nil
			}
			if err := repositories.Tasks().Create(request.Context(), task); err != nil {
				return err
			}
			created = true
			return nil
		},
	)
	if err != nil {
		return storageError(err)
	}
	document, err = decodeTask(task, session.Namespace)
	if err != nil {
		return internalError(err)
	}
	location := fmt.Sprintf(
		"%s/sessions/%s/mirrors/%s?namespace=%s",
		controlplane.APIPathPrefix, session.ID, task.ID, session.Namespace,
	)
	ctx.Response().Header().Set("Location", location)
	if !created {
		ctx.Response().Header().Set("Idempotent-Replayed", "true")
	}
	writeJSON(
		ctx,
		map[bool]int{true: http.StatusCreated, false: http.StatusOK}[created],
		document,
	)
	return nil
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
			fmt.Errorf("stored Mirror Task has invalid state %q", task.State),
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

func normalizeRequest(spec *Spec) *controlplaneapi.Error {
	spec.Service = strings.TrimSpace(spec.Service)
	if len(validation.IsDNS1123Subdomain(spec.Service)) != 0 {
		return invalid("service", "Service name is invalid")
	}
	if len(spec.Ports) == 0 || len(spec.Ports) > 64 {
		return invalid("ports", "one to 64 Service ports are required")
	}
	seen := make(map[string]struct{}, len(spec.Ports))
	for index := range spec.Ports {
		port := &spec.Ports[index]
		port.Name = strings.TrimSpace(port.Name)
		port.Protocol = strings.ToLower(strings.TrimSpace(port.Protocol))
		if port.ServicePort < 1 || port.ServicePort > 65535 ||
			(port.Protocol != "tcp" && port.Protocol != "udp") {
			return invalid("ports", "Service port and protocol are invalid")
		}
		key := fmt.Sprintf("%s/%d", port.Protocol, port.ServicePort)
		if _, exists := seen[key]; exists {
			return invalid("ports", "Service ports must be unique")
		}
		seen[key] = struct{}{}
	}
	slices.SortFunc(spec.Ports, comparePorts)
	return nil
}

func comparePorts(left, right trafficmodel.Port) int {
	if left.ServicePort != right.ServicePort {
		return int(left.ServicePort - right.ServicePort)
	}
	return strings.Compare(left.Protocol, right.Protocol)
}

func decodeTask(task storage.Task, namespace string) (Document, error) {
	var spec storedSpec
	if task.Type != TaskType || json.Unmarshal(task.Spec, &spec) != nil ||
		spec.Service == "" ||
		spec.ClusterIP == "" ||
		len(spec.Ports) == 0 {
		return Document{}, errors.New("stored Mirror Task is invalid")
	}
	return documentFrom(task, namespace, spec), nil
}

func documentFrom(
	task storage.Task,
	namespace string,
	spec storedSpec,
) Document {
	expiresAt := time.Time{}
	if task.ExpiresAt != nil {
		expiresAt = task.ExpiresAt.UTC()
	}
	return Document{
		ID: task.ID, SessionID: task.SessionID, Namespace: namespace, State: task.State,
		Service: spec.Service, ClusterIP: spec.ClusterIP, Ports: append([]trafficmodel.Port(nil), spec.Ports...),
		CreatedAt: task.CreatedAt.UTC(), UpdatedAt: task.UpdatedAt.UTC(), ExpiresAt: expiresAt,
	}
}

func owned(
	task storage.Task,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
) bool {
	return task.Type == TaskType && task.IdentityID == identity.Subject &&
		task.SessionID == session.ID
}

func namespaceFromQuery(
	request *http.Request,
) (string, *controlplaneapi.Error) {
	query := request.URL.Query()
	if len(query) != 1 || len(query["namespace"]) != 1 ||
		len(validation.IsDNS1123Label(query.Get("namespace"))) != 0 {
		return "", invalid(
			"namespace",
			"one valid namespace query parameter is required",
		)
	}
	return query.Get("namespace"), nil
}

func targetError(err error) *controlplaneapi.Error {
	switch {
	case apierrors.IsForbidden(err):
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeForbidden,
			Message: "Kubernetes Mirror access is not permitted",
			Cause:   err,
		}
	case apierrors.IsNotFound(err):
		return notFound()
	default:
		return invalid("service", err.Error())
	}
}

func storageError(err error) *controlplaneapi.Error {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return notFound()
	case errors.Is(err, storage.ErrConflict),
		errors.Is(err, storage.ErrIdempotencyMismatch):
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeConflict,
			Message: "Mirror Task conflicts with existing state",
			Cause:   err,
		}
	default:
		return internalError(err)
	}
}

func invalid(field, message string) *controlplaneapi.Error {
	return &controlplaneapi.Error{
		Code:    controlplaneapi.CodeInvalidArgument,
		Field:   field,
		Message: message,
	}
}

func notFound() *controlplaneapi.Error {
	return &controlplaneapi.Error{
		Code:    controlplaneapi.CodeNotFound,
		Message: "resource not found",
	}
}

func internalError(err error) *controlplaneapi.Error {
	return &controlplaneapi.Error{
		Code:    controlplaneapi.CodeInternal,
		Message: "Mirror operation failed",
		Cause:   err,
	}
}

func writeJSON(ctx *echo.Context, status int, value any) {
	_ = ctx.JSON(status, value)
}
