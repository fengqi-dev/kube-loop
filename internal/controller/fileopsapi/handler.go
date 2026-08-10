package fileopsapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controller"
	"github.com/fengqi-dev/kube-loop/internal/controller/fileapi"
	"github.com/fengqi-dev/kube-loop/internal/controller/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controller/storage"
	"github.com/fengqi-dev/kube-loop/internal/controller/taskapi"
	"github.com/fengqi-dev/kube-loop/internal/remotetask"
	"github.com/google/uuid"
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
	RequireActive(context.Context, controller.Principal, string, string) (sessionapi.ActiveSession, *controller.APIError)
}

type Config struct {
	Now              func() time.Time
	AllowedPathRoots []string
}

type Handler struct {
	storage      Storage
	sessions     SessionValidator
	targets      fileapi.TargetResolver
	operator     Operator
	now          func() time.Time
	allowedRoots []string
}

type Spec struct {
	Action          string `json:"action"`
	Pod             string `json:"pod"`
	Container       string `json:"container,omitempty"`
	Path            string `json:"path"`
	Destination     string `json:"destination,omitempty"`
	Kind            string `json:"kind,omitempty"`
	Recursive       bool   `json:"recursive,omitempty"`
	allowedRoot     string
	destinationRoot string
}

type Entry struct {
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	Kind       string    `json:"kind"`
	Size       int64     `json:"size"`
	Mode       string    `json:"mode"`
	ModifiedAt time.Time `json:"modifiedAt"`
}

type ListDocument struct {
	SessionID string  `json:"sessionId"`
	Namespace string  `json:"namespace"`
	Pod       string  `json:"pod"`
	Container string  `json:"container"`
	Path      string  `json:"path"`
	Items     []Entry `json:"items"`
}

type Result struct {
	Completed bool   `json:"completed"`
	Error     string `json:"error,omitempty"`
}

type Document struct {
	ID          string           `json:"id"`
	SessionID   string           `json:"sessionId"`
	Namespace   string           `json:"namespace"`
	State       remotetask.State `json:"state"`
	Action      string           `json:"action"`
	Pod         string           `json:"pod"`
	Container   string           `json:"container"`
	Path        string           `json:"path"`
	Destination string           `json:"destination,omitempty"`
	Kind        string           `json:"kind,omitempty"`
	Recursive   bool             `json:"recursive,omitempty"`
	Result      Result           `json:"result"`
	CreatedAt   time.Time        `json:"createdAt"`
	UpdatedAt   time.Time        `json:"updatedAt"`
	ExpiresAt   time.Time        `json:"expiresAt"`
}

func New(storageBackend Storage, sessions SessionValidator, targets fileapi.TargetResolver, operator Operator, config Config) (*Handler, error) {
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
	return &Handler{storage: storageBackend, sessions: sessions, targets: targets, operator: operator, now: config.Now, allowedRoots: roots}, nil
}

func (handler *Handler) ServeAPI(writer http.ResponseWriter, request *http.Request, principal controller.Principal) *controller.APIError {
	parts, ok := routeParts(request.URL.Path)
	if !ok {
		return notFound()
	}
	namespace, apiError := namespaceFromQuery(request)
	if apiError != nil {
		return apiError
	}
	session, apiError := handler.sessions.RequireActive(request.Context(), principal, namespace, parts[1])
	if apiError != nil {
		return apiError
	}
	controller.SetAuditSessionID(request.Context(), session.ID)
	switch {
	case len(parts) == 4 && parts[3] == "list" && request.Method == http.MethodPost:
		return handler.list(writer, request, principal, session)
	case len(parts) == 4 && (parts[3] == ActionCreate || parts[3] == ActionRename || parts[3] == ActionDelete) && request.Method == http.MethodPost:
		return handler.mutate(writer, request, principal, session, parts[3])
	case len(parts) == 5 && parts[3] == "operations" && request.Method == http.MethodGet:
		return handler.get(writer, request, principal, session, parts[4])
	default:
		return notFound()
	}
}

func (handler *Handler) list(writer http.ResponseWriter, request *http.Request, principal controller.Principal, session sessionapi.ActiveSession) *controller.APIError {
	spec := Spec{}
	if apiError := controller.DecodeJSON(request, &spec); apiError != nil {
		return apiError
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
		return &controller.APIError{Code: controller.CodeUnavailable, Message: "remote directory could not be read", Cause: err}
	}
	writeJSON(writer, http.StatusOK, ListDocument{
		SessionID: session.ID, Namespace: session.Namespace, Pod: spec.Pod, Container: container, Path: spec.Path, Items: items,
	})
	return nil
}

func (handler *Handler) mutate(writer http.ResponseWriter, request *http.Request, principal controller.Principal, session sessionapi.ActiveSession, action string) *controller.APIError {
	spec := Spec{}
	if apiError := controller.DecodeJSON(request, &spec); apiError != nil {
		return apiError
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
		writer.Header().Set("Idempotent-Replayed", "true")
		writeJSON(writer, http.StatusOK, document)
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
		writer.Header().Set("Idempotent-Replayed", "true")
	}
	document, err := decodeTask(task, session.Namespace)
	if err != nil {
		return internalError(err)
	}
	writer.Header().Set("Location", fmt.Sprintf("%s/sessions/%s/pod-files/operations/%s?namespace=%s", controller.APIPathPrefix, session.ID, task.ID, session.Namespace))
	writeJSON(writer, map[bool]int{true: http.StatusCreated, false: http.StatusOK}[created], document)
	return nil
}

func (handler *Handler) execute(ctx context.Context, principal controller.Principal, namespace string, task storage.Task, spec Spec) (storage.Task, error) {
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

func (handler *Handler) replay(ctx context.Context, scope, key, hash string, principal controller.Principal, session sessionapi.ActiveSession) (storage.Task, bool, *controller.APIError) {
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

func (handler *Handler) get(writer http.ResponseWriter, request *http.Request, principal controller.Principal, session sessionapi.ActiveSession, taskID string) *controller.APIError {
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
	writeJSON(writer, http.StatusOK, document)
	return nil
}

func (handler *Handler) normalize(spec *Spec) *controller.APIError {
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
	spec.Path, spec.allowedRoot = normalized, root
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
		spec.Destination, spec.destinationRoot = destination, destinationRoot
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

func owned(task storage.Task, principal controller.Principal, session sessionapi.ActiveSession) bool {
	return task.Type == TaskType && task.PrincipalID == principal.Subject && task.SessionID == session.ID
}

func routeParts(requestPath string) ([]string, bool) {
	suffix, ok := strings.CutPrefix(requestPath, controller.APIPathPrefix+"/sessions/")
	if !ok || strings.HasSuffix(suffix, "/") || strings.Contains(suffix, "//") {
		return nil, false
	}
	parts := strings.Split(suffix, "/")
	if len(parts) < 3 || parts[1] != "pod-files" {
		return nil, false
	}
	return append([]string{"sessions"}, parts...), true
}

func namespaceFromQuery(request *http.Request) (string, *controller.APIError) {
	query := request.URL.Query()
	if len(query) != 1 || len(query["namespace"]) != 1 || len(validation.IsDNS1123Label(query.Get("namespace"))) != 0 {
		return "", invalid("namespace", "one valid namespace query parameter is required")
	}
	return query.Get("namespace"), nil
}

func storageError(err error) *controller.APIError {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return notFound()
	case errors.Is(err, storage.ErrIdempotencyMismatch):
		return &controller.APIError{Code: controller.CodeConflict, Message: "Idempotency-Key was already used for a different request", Cause: err}
	case errors.Is(err, storage.ErrConflict):
		return &controller.APIError{Code: controller.CodeConflict, Message: "remote file Task state changed; reload and retry", Cause: err}
	default:
		return internalError(err)
	}
}

func targetError(err error) *controller.APIError {
	return &controller.APIError{Code: controller.CodeInvalidArgument, Message: "Pod file target is unavailable", Cause: err}
}

func invalid(field, message string) *controller.APIError {
	return &controller.APIError{Code: controller.CodeInvalidArgument, Field: field, Message: message}
}

func internalError(err error) *controller.APIError {
	return &controller.APIError{Code: controller.CodeInternal, Message: "remote file operation failed", Cause: err}
}

func notFound() *controller.APIError {
	return &controller.APIError{Code: controller.CodeNotFound, Message: "resource not found"}
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
