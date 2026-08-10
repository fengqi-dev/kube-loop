package previewapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controller"
	"github.com/fengqi-dev/kube-loop/internal/controller/exchangeapi"
	"github.com/fengqi-dev/kube-loop/internal/controller/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controller/storage"
	"github.com/fengqi-dev/kube-loop/internal/controller/taskapi"
	"github.com/fengqi-dev/kube-loop/internal/remotetask"
	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/util/validation"
)

const TaskType = "preview"

type Storage interface {
	storage.Repositories
	storage.TransactionManager
}

type SessionValidator interface {
	RequireActive(context.Context, controller.Principal, string, string) (sessionapi.ActiveSession, *controller.APIError)
}

type Port = exchangeapi.Port

type Spec struct {
	Name  string `json:"name"`
	Ports []Port `json:"ports"`
}

type Document struct {
	ID        string           `json:"id"`
	SessionID string           `json:"sessionId"`
	Namespace string           `json:"namespace"`
	State     remotetask.State `json:"state"`
	Name      string           `json:"name"`
	ClusterIP string           `json:"clusterIp,omitempty"`
	Ports     []Port           `json:"ports"`
	CreatedAt time.Time        `json:"createdAt"`
	UpdatedAt time.Time        `json:"updatedAt"`
	ExpiresAt time.Time        `json:"expiresAt"`
}

type storedSpec struct {
	Name  string `json:"name"`
	Ports []Port `json:"ports"`
}

type ownerResult struct {
	OwnerID   string `json:"ownerId"`
	GatewayIP string `json:"gatewayIp"`
	ClusterIP string `json:"clusterIp,omitempty"`
	Deleted   bool   `json:"deleted,omitempty"`
	Error     string `json:"error,omitempty"`
}

type Handler struct {
	storage   Storage
	sessions  SessionValidator
	resources ResourceManager
	now       func() time.Time
	config    Config
}

func New(storageBackend Storage, sessions SessionValidator, resources ResourceManager, config Config) (*Handler, error) {
	if storageBackend == nil || sessions == nil || resources == nil {
		return nil, errors.New("Preview storage, Session validator and resource manager are required")
	}
	if err := config.normalize(); err != nil {
		return nil, err
	}
	return &Handler{storage: storageBackend, sessions: sessions, resources: resources, now: config.Now, config: config}, nil
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
	case len(parts) == 3 && request.Method == http.MethodPost:
		return handler.create(writer, request, principal, session)
	case len(parts) == 4 && request.Method == http.MethodGet:
		return handler.get(writer, request, principal, session, parts[3])
	case len(parts) == 4 && request.Method == http.MethodDelete:
		return handler.stop(writer, request, principal, session, parts[3])
	case len(parts) == 5 && parts[4] == "stream" && request.Method == http.MethodGet:
		return handler.stream(writer, request, principal, session, parts[3])
	default:
		return notFound()
	}
}

func (handler *Handler) create(
	writer http.ResponseWriter,
	request *http.Request,
	principal controller.Principal,
	session sessionapi.ActiveSession,
) *controller.APIError {
	var spec Spec
	if apiError := controller.DecodeJSON(request, &spec); apiError != nil {
		return apiError
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
	scope := taskapi.Scope(TaskType, principal.Subject)
	if record, err := handler.storage.Idempotency().Get(request.Context(), scope, key); err == nil {
		if record.RequestHash != requestHash {
			return storageError(storage.ErrIdempotencyMismatch)
		}
		task, err := handler.storage.Tasks().GetByID(request.Context(), record.ResourceID)
		if err != nil || !owned(task, principal, session) {
			return notFound()
		}
		document, err := decodeTask(task, session.Namespace)
		if err != nil {
			return internalError(err)
		}
		writer.Header().Set("Idempotent-Replayed", "true")
		writeJSON(writer, http.StatusOK, document)
		return nil
	} else if !errors.Is(err, storage.ErrNotFound) {
		return storageError(err)
	}
	canonical := storedSpec{Name: spec.Name, Ports: append([]Port(nil), spec.Ports...)}
	specJSON, _ := json.Marshal(canonical)
	now := handler.now().UTC()
	expiresAt := session.ExpiresAt.UTC()
	task := storage.Task{
		ID: uuid.NewString(), PrincipalID: principal.Subject, SessionID: session.ID,
		Type: TaskType, State: remotetask.Pending, Spec: specJSON, IdempotencyKey: key,
		CreatedAt: now, UpdatedAt: now, ExpiresAt: &expiresAt,
	}
	document := documentFrom(task, session.Namespace, canonical)
	response, _ := json.Marshal(document)
	created := false
	err = handler.storage.WithinTransaction(request.Context(), func(repositories storage.Repositories) error {
		record, reserved, err := repositories.Idempotency().Reserve(request.Context(), storage.IdempotencyRecord{
			Scope: scope, Key: key, RequestHash: requestHash, ResourceType: TaskType,
			ResourceID: task.ID, Response: response, CreatedAt: now, ExpiresAt: expiresAt,
		})
		if err != nil {
			return err
		}
		if !reserved {
			existing, err := repositories.Tasks().GetByID(request.Context(), record.ResourceID)
			if err != nil || !owned(existing, principal, session) {
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
	})
	if err != nil {
		return storageError(err)
	}
	document, err = decodeTask(task, session.Namespace)
	if err != nil {
		return internalError(err)
	}
	writer.Header().Set("Location", fmt.Sprintf(
		"%s/sessions/%s/previews/%s/stream?namespace=%s",
		controller.APIPathPrefix, session.ID, task.ID, session.Namespace,
	))
	if !created {
		writer.Header().Set("Idempotent-Replayed", "true")
	}
	writeJSON(writer, map[bool]int{true: http.StatusCreated, false: http.StatusOK}[created], document)
	return nil
}

func (handler *Handler) get(
	writer http.ResponseWriter,
	request *http.Request,
	principal controller.Principal,
	session sessionapi.ActiveSession,
	taskID string,
) *controller.APIError {
	task, apiError := handler.ownedTask(request.Context(), principal, session, taskID)
	if apiError != nil {
		return apiError
	}
	document, err := decodeTask(task, session.Namespace)
	if err != nil {
		return internalError(err)
	}
	writeJSON(writer, http.StatusOK, document)
	return nil
}

func (handler *Handler) stop(
	writer http.ResponseWriter,
	request *http.Request,
	principal controller.Principal,
	session sessionapi.ActiveSession,
	taskID string,
) *controller.APIError {
	task, apiError := handler.ownedTask(request.Context(), principal, session, taskID)
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
		return internalError(fmt.Errorf("stored Preview Task has invalid state %q", task.State))
	}
	if next != task.State {
		result := task.Result
		if len(result) == 0 {
			result, _ = json.Marshal(map[string]bool{"stopRequested": true})
		}
		now := handler.now().UTC()
		if err := handler.storage.Tasks().UpdateState(request.Context(), task.ID, task.State, next, result, now); err != nil {
			return storageError(err)
		}
		task.State, task.Result, task.UpdatedAt = next, result, now
	}
	document, err := decodeTask(task, session.Namespace)
	if err != nil {
		return internalError(err)
	}
	writeJSON(writer, map[bool]int{true: http.StatusAccepted, false: http.StatusOK}[next == remotetask.Stopping], document)
	return nil
}

func (handler *Handler) ownedTask(
	ctx context.Context,
	principal controller.Principal,
	session sessionapi.ActiveSession,
	taskID string,
) (storage.Task, *controller.APIError) {
	if _, err := uuid.Parse(taskID); err != nil {
		return storage.Task{}, notFound()
	}
	task, err := handler.storage.Tasks().GetByID(ctx, taskID)
	if err != nil || !owned(task, principal, session) {
		return storage.Task{}, notFound()
	}
	return task, nil
}

func normalizeRequest(spec *Spec) *controller.APIError {
	spec.Name = strings.TrimSpace(spec.Name)
	if len(validation.IsDNS1123Label(spec.Name)) != 0 {
		return invalid("name", "Service name is invalid")
	}
	if len(spec.Ports) == 0 || len(spec.Ports) > 64 {
		return invalid("ports", "one to 64 Service ports are required")
	}
	seenPorts := make(map[string]struct{}, len(spec.Ports))
	seenNames := make(map[string]struct{}, len(spec.Ports))
	for index := range spec.Ports {
		port := &spec.Ports[index]
		port.Name = strings.TrimSpace(port.Name)
		port.Protocol = strings.ToLower(strings.TrimSpace(port.Protocol))
		if port.ServicePort < 1 || port.ServicePort > 65535 || (port.Protocol != "tcp" && port.Protocol != "udp") {
			return invalid("ports", "Service port and protocol are invalid")
		}
		if port.Name == "" {
			port.Name = fmt.Sprintf("%s-%d", port.Protocol, port.ServicePort)
		}
		if len(validation.IsDNS1123Label(port.Name)) != 0 {
			return invalid("ports", "Service port name is invalid")
		}
		key := fmt.Sprintf("%s/%d", port.Protocol, port.ServicePort)
		if _, exists := seenPorts[key]; exists {
			return invalid("ports", "Service ports must be unique")
		}
		if _, exists := seenNames[port.Name]; exists {
			return invalid("ports", "Service port names must be unique")
		}
		seenPorts[key], seenNames[port.Name] = struct{}{}, struct{}{}
	}
	slices.SortFunc(spec.Ports, comparePorts)
	return nil
}

func comparePorts(left, right Port) int {
	if left.ServicePort != right.ServicePort {
		return int(left.ServicePort - right.ServicePort)
	}
	return strings.Compare(left.Protocol, right.Protocol)
}

func decodeTask(task storage.Task, namespace string) (Document, error) {
	var spec storedSpec
	if task.Type != TaskType || json.Unmarshal(task.Spec, &spec) != nil || spec.Name == "" || len(spec.Ports) == 0 {
		return Document{}, errors.New("stored Preview Task is invalid")
	}
	return documentFrom(task, namespace, spec), nil
}

func documentFrom(task storage.Task, namespace string, spec storedSpec) Document {
	expiresAt := time.Time{}
	if task.ExpiresAt != nil {
		expiresAt = task.ExpiresAt.UTC()
	}
	result := ownerResult{}
	_ = json.Unmarshal(task.Result, &result)
	return Document{
		ID: task.ID, SessionID: task.SessionID, Namespace: namespace, State: task.State,
		Name: spec.Name, ClusterIP: result.ClusterIP, Ports: append([]Port(nil), spec.Ports...),
		CreatedAt: task.CreatedAt.UTC(), UpdatedAt: task.UpdatedAt.UTC(), ExpiresAt: expiresAt,
	}
}

func owned(task storage.Task, principal controller.Principal, session sessionapi.ActiveSession) bool {
	return task.Type == TaskType && task.PrincipalID == principal.Subject && task.SessionID == session.ID
}

func routeParts(path string) ([]string, bool) {
	suffix, ok := strings.CutPrefix(path, controller.APIPathPrefix+"/sessions/")
	if !ok || strings.HasSuffix(suffix, "/") || strings.Contains(suffix, "//") {
		return nil, false
	}
	parts := strings.Split(suffix, "/")
	if len(parts) < 2 || parts[1] != "previews" {
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
	case errors.Is(err, storage.ErrConflict), errors.Is(err, storage.ErrIdempotencyMismatch):
		return &controller.APIError{Code: controller.CodeConflict, Message: "Preview Task conflicts with existing state", Cause: err}
	default:
		return internalError(err)
	}
}

func invalid(field, message string) *controller.APIError {
	return &controller.APIError{Code: controller.CodeInvalidArgument, Field: field, Message: message}
}

func notFound() *controller.APIError {
	return &controller.APIError{Code: controller.CodeNotFound, Message: "resource not found"}
}

func internalError(err error) *controller.APIError {
	return &controller.APIError{Code: controller.CodeInternal, Message: "Preview operation failed", Cause: err}
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
