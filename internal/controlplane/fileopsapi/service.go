package fileopsapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/fileapi"
	controlplanemiddleware "github.com/fengqi-dev/kube-loop/internal/controlplane/middleware"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/taskapi"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
	"k8s.io/apimachinery/pkg/util/validation"
)

const (
	TaskType      = "pod-file-operation"
	ActionCreate  = "create"
	ActionRename  = "rename"
	ActionDelete  = "delete"
	KindFile      = "file"
	KindDirectory = "directory"
	KindSymlink   = "symlink"
	KindOther     = "other"
)

type Storage interface {
	storage.Repositories
	storage.TransactionManager
}

type SessionValidator interface {
	RequireActive(context.Context, controlplaneapi.Principal, string, string) (sessionapi.ActiveSession, *controlplaneapi.Error)
}

type Config struct {
	Now              func() time.Time
	AllowedPathRoots []string
}

type Service struct {
	storage      Storage
	sessions     SessionValidator
	targets      fileapi.TargetResolver
	operator     Operator
	now          func() time.Time
	allowedRoots []string
}

func New(storageBackend Storage, sessions SessionValidator, targets fileapi.TargetResolver, operator Operator, config Config) (*Service, error) {
	if storageBackend == nil || sessions == nil || targets == nil || operator == nil {
		return nil, errors.New("remote file storage, Session validator, target resolver and operator are required")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	roots, err := fileapi.NormalizeAllowedRoots(config.AllowedPathRoots)
	if err != nil {
		return nil, err
	}
	return &Service{storage: storageBackend, sessions: sessions, targets: targets, operator: operator, now: config.Now, allowedRoots: roots}, nil
}

func (handler *Service) list(ctx *echo.Context, principal controlplaneapi.Principal, session sessionapi.ActiveSession) *controlplaneapi.Error {
	request := ctx.Request()
	spec := Spec{}
	if err := ctx.Bind(&spec); err != nil {
		return controlplanemiddleware.BindingError(err)
	}
	spec.Action = "list"
	if apiError := handler.normalize(&spec); apiError != nil {
		return apiError
	}
	container, err := handler.targets.ResolveContainer(request.Context(), principal, session.Namespace, spec.Pod, spec.Container)
	if err != nil {
		return targetError(err)
	}
	spec.Container = container
	items, err := handler.operator.List(request.Context(), principal, session.Namespace, spec)
	if err != nil {
		return &controlplaneapi.Error{Code: controlplaneapi.CodeUnavailable, Message: "remote directory could not be read", Cause: err}
	}
	writeJSON(ctx, http.StatusOK, ListDocument{
		SessionID: session.ID, Namespace: session.Namespace, Pod: spec.Pod, Container: container, Path: spec.Path, Items: items,
	})
	return nil
}

func (handler *Service) mutate(ctx *echo.Context, principal controlplaneapi.Principal, session sessionapi.ActiveSession, action string) *controlplaneapi.Error {
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
	scope := taskapi.Scope(TaskType, principal.Subject)
	if task, replayed, apiError := handler.replay(request.Context(), scope, key, requestHash, principal, session); apiError != nil {
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
	container, err := handler.targets.ResolveContainer(request.Context(), principal, session.Namespace, spec.Pod, spec.Container)
	if err != nil {
		return targetError(err)
	}
	spec.Container = container
	specJSON, _ := json.Marshal(spec)
	now, expiresAt := handler.now().UTC(), session.ExpiresAt.UTC()
	task := storage.Task{
		ID: uuid.NewString(), PrincipalID: principal.Subject, SessionID: session.ID, Type: TaskType, State: remotetask.Pending,
		Spec: specJSON, IdempotencyKey: key, CreatedAt: now, UpdatedAt: now, ExpiresAt: &expiresAt,
	}
	created := false
	err = handler.storage.WithinTransaction(request.Context(), func(repositories storage.Repositories) error {
		record, reserved, reserveErr := repositories.Idempotency().Reserve(request.Context(), storage.IdempotencyRecord{
			Scope: scope, Key: key, RequestHash: requestHash, ResourceType: TaskType, ResourceID: task.ID,
			CreatedAt: now, ExpiresAt: expiresAt,
		})
		if reserveErr != nil {
			return reserveErr
		}
		if !reserved {
			existing, loadErr := repositories.Tasks().GetByID(request.Context(), record.ResourceID)
			if loadErr != nil || !owned(existing, principal, session) {
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
	})
	if err != nil {
		return storageError(err)
	}
	if created {
		task, err = handler.execute(request.Context(), principal, session.Namespace, task, spec)
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
	ctx.Response().Header().Set("Location", fmt.Sprintf("%s/sessions/%s/pod-files/operations/%s?namespace=%s", controlplane.APIPathPrefix, session.ID, task.ID, session.Namespace))
	writeJSON(ctx, map[bool]int{true: http.StatusCreated, false: http.StatusOK}[created], document)
	return nil
}

func (handler *Service) execute(ctx context.Context, principal controlplaneapi.Principal, namespace string, task storage.Task, spec Spec) (storage.Task, error) {
	if err := handler.storage.Tasks().UpdateState(ctx, task.ID, remotetask.Pending, remotetask.Running, json.RawMessage(`{}`), handler.now().UTC()); err != nil {
		return storage.Task{}, err
	}
	next := remotetask.Stopped
	result := Result{Completed: true}
	if err := handler.operator.Mutate(ctx, principal, namespace, spec); err != nil {
		next = remotetask.Failed
		result = Result{Error: "remote file operation failed"}
	}
	encoded, _ := json.Marshal(result)
	if err := handler.storage.Tasks().UpdateState(ctx, task.ID, remotetask.Running, next, encoded, handler.now().UTC()); err != nil {
		return storage.Task{}, err
	}
	return handler.storage.Tasks().GetByID(ctx, task.ID)
}

func (handler *Service) replay(ctx context.Context, scope, key, hash string, principal controlplaneapi.Principal, session sessionapi.ActiveSession) (storage.Task, bool, *controlplaneapi.Error) {
	record, err := handler.storage.Idempotency().Get(ctx, scope, key)
	if errors.Is(err, storage.ErrNotFound) {
		return storage.Task{}, false, nil
	}
	if err != nil {
		return storage.Task{}, false, storageError(err)
	}
	if record.RequestHash != hash {
		return storage.Task{}, false, storageError(storage.ErrIdempotencyMismatch)
	}
	task, err := handler.storage.Tasks().GetByID(ctx, record.ResourceID)
	if err != nil || !owned(task, principal, session) {
		return storage.Task{}, false, notFound()
	}
	return task, true, nil
}

func (handler *Service) get(ctx *echo.Context, principal controlplaneapi.Principal, session sessionapi.ActiveSession, taskID string) *controlplaneapi.Error {
	request := ctx.Request()
	if _, err := uuid.Parse(taskID); err != nil {
		return notFound()
	}
	task, err := handler.storage.Tasks().GetByID(request.Context(), taskID)
	if err != nil || !owned(task, principal, session) {
		return notFound()
	}
	document, err := decodeTask(task, session.Namespace)
	if err != nil {
		return internalError(err)
	}
	writeJSON(ctx, http.StatusOK, document)
	return nil
}

func (handler *Service) normalize(spec *Spec) *controlplaneapi.Error {
	spec.Pod, spec.Container = strings.TrimSpace(spec.Pod), strings.TrimSpace(spec.Container)
	if len(validation.IsDNS1123Subdomain(spec.Pod)) != 0 {
		return invalid("pod", "Pod name is invalid")
	}
	if spec.Container != "" && len(validation.IsDNS1123Label(spec.Container)) != 0 {
		return invalid("container", "container name is invalid")
	}
	normalized, root, err := fileapi.NormalizeContainerPath(spec.Path, handler.allowedRoots)
	if err != nil {
		return invalid("path", err.Error())
	}
	spec.Path, spec.AllowedRoot = normalized, root
	spec.Kind = strings.ToLower(strings.TrimSpace(spec.Kind))
	switch spec.Action {
	case "list":
		if spec.Destination != "" || spec.Kind != "" || spec.Recursive {
			return invalid("path", "list accepts only pod, container and path")
		}
	case ActionCreate:
		if spec.Path == root {
			return invalid("path", "configured allowed roots cannot be modified")
		}
		if spec.Kind != KindFile && spec.Kind != KindDirectory {
			return invalid("kind", "kind must be file or directory")
		}
		if spec.Destination != "" || spec.Recursive {
			return invalid("destination", "create does not accept destination or recursive")
		}
	case ActionRename:
		if spec.Path == root {
			return invalid("path", "configured allowed roots cannot be modified")
		}
		destination, destinationRoot, destinationErr := fileapi.NormalizeContainerPath(spec.Destination, handler.allowedRoots)
		if destinationErr != nil {
			return invalid("destination", destinationErr.Error())
		}
		if destination == destinationRoot {
			return invalid("destination", "configured allowed roots cannot be modified")
		}
		if destination == spec.Path {
			return invalid("destination", "destination must differ from path")
		}
		spec.Destination, spec.DestinationRoot = destination, destinationRoot
		if spec.Kind != "" || spec.Recursive {
			return invalid("kind", "rename does not accept kind or recursive")
		}
	case ActionDelete:
		if spec.Path == root {
			return invalid("path", "configured allowed roots cannot be modified")
		}
		if spec.Destination != "" || spec.Kind != "" {
			return invalid("destination", "delete does not accept destination or kind")
		}
	default:
		return invalid("action", "remote file action is invalid")
	}
	return nil
}

func decodeTask(task storage.Task, namespace string) (Document, error) {
	var spec Spec
	if err := json.Unmarshal(task.Spec, &spec); err != nil {
		return Document{}, err
	}
	result := Result{}
	if len(task.Result) > 0 {
		if err := json.Unmarshal(task.Result, &result); err != nil {
			return Document{}, err
		}
	}
	expiresAt := time.Time{}
	if task.ExpiresAt != nil {
		expiresAt = task.ExpiresAt.UTC()
	}
	return Document{
		ID: task.ID, SessionID: task.SessionID, Namespace: namespace, State: task.State, Action: spec.Action,
		Pod: spec.Pod, Container: spec.Container, Path: spec.Path, Destination: spec.Destination, Kind: spec.Kind,
		Recursive: spec.Recursive, Result: result, CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt, ExpiresAt: expiresAt,
	}, nil
}

func owned(task storage.Task, principal controlplaneapi.Principal, session sessionapi.ActiveSession) bool {
	return task.Type == TaskType && task.PrincipalID == principal.Subject && task.SessionID == session.ID
}

func namespaceFromQuery(request *http.Request) (string, *controlplaneapi.Error) {
	query := request.URL.Query()
	if len(query) != 1 || len(query["namespace"]) != 1 || len(validation.IsDNS1123Label(query.Get("namespace"))) != 0 {
		return "", invalid("namespace", "one valid namespace query parameter is required")
	}
	return query.Get("namespace"), nil
}

func storageError(err error) *controlplaneapi.Error {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return notFound()
	case errors.Is(err, storage.ErrIdempotencyMismatch):
		return &controlplaneapi.Error{Code: controlplaneapi.CodeConflict, Message: "Idempotency-Key was already used for a different request", Cause: err}
	case errors.Is(err, storage.ErrConflict):
		return &controlplaneapi.Error{Code: controlplaneapi.CodeConflict, Message: "remote file Task state changed; reload and retry", Cause: err}
	default:
		return internalError(err)
	}
}

func targetError(err error) *controlplaneapi.Error {
	return &controlplaneapi.Error{Code: controlplaneapi.CodeInvalidArgument, Message: "Pod file target is unavailable", Cause: err}
}

func invalid(field, message string) *controlplaneapi.Error {
	return &controlplaneapi.Error{Code: controlplaneapi.CodeInvalidArgument, Field: field, Message: message}
}

func internalError(err error) *controlplaneapi.Error {
	return &controlplaneapi.Error{Code: controlplaneapi.CodeInternal, Message: "remote file operation failed", Cause: err}
}

func notFound() *controlplaneapi.Error {
	return &controlplaneapi.Error{Code: controlplaneapi.CodeNotFound, Message: "resource not found"}
}

func writeJSON(ctx *echo.Context, status int, value any) {
	_ = ctx.JSON(status, value)
}
