package portforwardapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controller"
	"github.com/fengqi-dev/kube-loop/internal/controller/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controller/storage"
	"github.com/fengqi-dev/kube-loop/internal/controller/taskapi"
	"github.com/fengqi-dev/kube-loop/internal/remotetask"
	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/util/validation"
)

const TaskType = "port-forward"

type Storage interface {
	storage.Repositories
	storage.TransactionManager
}

type SessionValidator interface {
	RequireActive(context.Context, controller.Principal, string, string) (sessionapi.ActiveSession, *controller.APIError)
}

type Resolver interface {
	Resolve(context.Context, controller.Principal, string, Spec) (Target, error)
}

type Config struct {
	Now func() time.Time
}

type Handler struct {
	storage  Storage
	sessions SessionValidator
	resolver Resolver
	bindings BindingManager
	now      func() time.Time
}

type Spec struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Protocol   string `json:"protocol"`
	RemotePort uint16 `json:"remotePort"`
}

type Target struct {
	Host string `json:"host"`
	Port uint16 `json:"port"`
}

func (target Target) Address() string {
	return net.JoinHostPort(target.Host, strconv.Itoa(int(target.Port)))
}

type Document struct {
	ID          string           `json:"id"`
	SessionID   string           `json:"sessionId"`
	Namespace   string           `json:"namespace"`
	State       remotetask.State `json:"state"`
	Kind        string           `json:"kind"`
	Name        string           `json:"name"`
	Protocol    string           `json:"protocol"`
	RemotePort  uint16           `json:"remotePort"`
	DialAddress string           `json:"dialAddress"`
	CreatedAt   time.Time        `json:"createdAt"`
	UpdatedAt   time.Time        `json:"updatedAt"`
	ExpiresAt   time.Time        `json:"expiresAt"`
}

type listDocument struct {
	Items []Document `json:"items"`
}

func New(
	storageBackend Storage,
	sessions SessionValidator,
	resolver Resolver,
	bindings BindingManager,
	config Config,
) (*Handler, error) {
	if storageBackend == nil || sessions == nil || resolver == nil || bindings == nil {
		return nil, errors.New("Port Forward storage, Session validator, target resolver and TrafficBinding manager are required")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Handler{storage: storageBackend, sessions: sessions, resolver: resolver, bindings: bindings, now: config.Now}, nil
}

func (handler *Handler) ServeAPI(writer http.ResponseWriter, request *http.Request, principal controller.Principal) *controller.APIError {
	parts, valid := routeParts(request.URL.Path)
	if !valid {
		return notFound()
	}
	namespace, apiError := namespaceFromQuery(request)
	if apiError != nil {
		return apiError
	}
	sessionID := parts[1]
	active, apiError := handler.sessions.RequireActive(request.Context(), principal, namespace, sessionID)
	if apiError != nil {
		return apiError
	}
	controller.SetAuditSessionID(request.Context(), active.ID)
	switch {
	case len(parts) == 3 && request.Method == http.MethodPost:
		return handler.create(writer, request, principal, active)
	case len(parts) == 3 && request.Method == http.MethodGet:
		return handler.list(writer, request, principal, active)
	case len(parts) == 4 && request.Method == http.MethodDelete:
		return handler.stop(writer, request, principal, active, parts[3])
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
	if apiError := normalizeSpec(&spec); apiError != nil {
		return apiError
	}
	idempotencyKey, apiError := taskapi.IdempotencyKey(request)
	if apiError != nil {
		return apiError
	}
	hashSource, _ := json.Marshal(struct {
		SessionID string `json:"sessionId"`
		Namespace string `json:"namespace"`
		Spec      Spec   `json:"spec"`
	}{SessionID: session.ID, Namespace: session.Namespace, Spec: spec})
	legacyRequestHash := taskapi.Hash(hashSource)
	requestHash, err := taskapi.RequestHash(session.ID, session.Namespace, spec)
	if err != nil {
		return internalError(err)
	}
	scope := taskapi.Scope(TaskType, principal.Subject)
	if record, getErr := handler.storage.Idempotency().Get(request.Context(), scope, idempotencyKey); getErr == nil {
		if !taskapi.Matches(record.RequestHash, requestHash, legacyRequestHash) {
			return mapStorageError(storage.ErrIdempotencyMismatch)
		}
		if record.ResourceType != TaskType {
			return mapStorageError(storage.ErrConflict)
		}
		existing, taskErr := handler.storage.Tasks().GetByID(request.Context(), record.ResourceID)
		if taskErr != nil {
			return mapStorageError(taskErr)
		}
		if existing.PrincipalID != principal.Subject || existing.SessionID != session.ID || existing.Type != TaskType {
			return notFound()
		}
		if apiError := handler.activate(request.Context(), session, &existing); apiError != nil {
			return apiError
		}
		document, decodeErr := decodeTask(existing, session.Namespace)
		if decodeErr != nil {
			return internalError(decodeErr)
		}
		writer.Header().Set("Location", fmt.Sprintf(
			"%s/sessions/%s/port-forwards/%s?namespace=%s",
			controller.APIPathPrefix, session.ID, existing.ID, session.Namespace,
		))
		writer.Header().Set("Idempotent-Replayed", "true")
		writeJSON(writer, http.StatusOK, document)
		return nil
	} else if !errors.Is(getErr, storage.ErrNotFound) {
		return mapStorageError(getErr)
	}
	target, err := handler.resolver.Resolve(request.Context(), principal, session.Namespace, spec)
	if err != nil {
		return &controller.APIError{Code: controller.CodeUnavailable, Message: "Kubernetes Port Forward target resolution failed", Cause: err}
	}
	if err := validateTarget(target); err != nil {
		return &controller.APIError{Code: controller.CodeInternal, Message: "Port Forward resolver returned an invalid target", Cause: err}
	}
	now := handler.now().UTC()
	specJSON, _ := json.Marshal(spec)
	targetJSON, _ := json.Marshal(target)
	expiresAt := session.ExpiresAt.UTC()
	task := storage.Task{
		ID: uuid.NewString(), PrincipalID: principal.Subject, SessionID: session.ID,
		Type: TaskType, State: remotetask.Pending, Spec: specJSON, Result: targetJSON,
		IdempotencyKey: idempotencyKey, CreatedAt: now, UpdatedAt: now, ExpiresAt: &expiresAt,
	}
	document := documentFromTask(task, session.Namespace)
	responseJSON, _ := json.Marshal(document)
	created := false
	err = handler.storage.WithinTransaction(request.Context(), func(repositories storage.Repositories) error {
		record, reserved, reserveErr := repositories.Idempotency().Reserve(request.Context(), storage.IdempotencyRecord{
			Scope: scope,
			Key:   idempotencyKey, RequestHash: requestHash, ResourceType: TaskType,
			ResourceID: task.ID, Response: responseJSON, CreatedAt: now, ExpiresAt: expiresAt,
		})
		if reserveErr != nil {
			return reserveErr
		}
		if !reserved {
			if record.ResourceType != TaskType {
				return storage.ErrConflict
			}
			existing, getErr := repositories.Tasks().GetByID(request.Context(), record.ResourceID)
			if getErr != nil {
				return getErr
			}
			if existing.PrincipalID != principal.Subject || existing.SessionID != session.ID || existing.Type != TaskType {
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
		return mapStorageError(err)
	}
	if apiError := handler.activate(request.Context(), session, &task); apiError != nil {
		return apiError
	}
	document, err = decodeTask(task, session.Namespace)
	if err != nil {
		return internalError(err)
	}
	writer.Header().Set("Location", fmt.Sprintf(
		"%s/sessions/%s/port-forwards/%s?namespace=%s",
		controller.APIPathPrefix, session.ID, task.ID, session.Namespace,
	))
	if !created {
		writer.Header().Set("Idempotent-Replayed", "true")
	}
	writeJSON(writer, map[bool]int{true: http.StatusCreated, false: http.StatusOK}[created], document)
	return nil
}

func (handler *Handler) list(
	writer http.ResponseWriter,
	request *http.Request,
	principal controller.Principal,
	session sessionapi.ActiveSession,
) *controller.APIError {
	if apiError := requireEmptyBody(request); apiError != nil {
		return apiError
	}
	tasks, err := handler.storage.Tasks().ListBySession(request.Context(), session.ID, 1000)
	if err != nil {
		return mapStorageError(err)
	}
	items := make([]Document, 0, len(tasks))
	for _, task := range tasks {
		if task.Type != TaskType || task.PrincipalID != principal.Subject {
			continue
		}
		document, decodeErr := decodeTask(task, session.Namespace)
		if decodeErr != nil {
			return internalError(decodeErr)
		}
		items = append(items, document)
	}
	writeJSON(writer, http.StatusOK, listDocument{Items: items})
	return nil
}

func (handler *Handler) stop(
	writer http.ResponseWriter,
	request *http.Request,
	principal controller.Principal,
	session sessionapi.ActiveSession,
	taskID string,
) *controller.APIError {
	if apiError := requireEmptyBody(request); apiError != nil {
		return apiError
	}
	if _, err := uuid.Parse(taskID); err != nil {
		return notFound()
	}
	task, err := handler.storage.Tasks().GetByID(request.Context(), taskID)
	if err != nil || task.Type != TaskType || task.PrincipalID != principal.Subject || task.SessionID != session.ID {
		if err != nil && !errors.Is(err, storage.ErrNotFound) {
			return mapStorageError(err)
		}
		return notFound()
	}
	if err := handler.bindings.Delete(request.Context(), session.Namespace, task.ID); err != nil {
		return &controller.APIError{Code: controller.CodeUnavailable, Message: "Port Forward cleanup is pending", Cause: err}
	}
	if !task.State.Terminal() {
		if err := handler.storage.Tasks().UpdateState(
			request.Context(), task.ID, task.State, remotetask.Stopped, task.Result, handler.now().UTC(),
		); err != nil {
			return mapStorageError(err)
		}
		task, err = handler.storage.Tasks().GetByID(request.Context(), task.ID)
		if err != nil {
			return mapStorageError(err)
		}
	}
	document, err := decodeTask(task, session.Namespace)
	if err != nil {
		return internalError(err)
	}
	writeJSON(writer, http.StatusOK, document)
	return nil
}

func (handler *Handler) activate(
	ctx context.Context,
	session sessionapi.ActiveSession,
	task *storage.Task,
) *controller.APIError {
	if task.State == remotetask.Running {
		return nil
	}
	if task.State != remotetask.Pending {
		return nil
	}
	var spec Spec
	if err := json.Unmarshal(task.Spec, &spec); err != nil {
		return internalError(err)
	}
	managed, err := handler.bindings.Activate(ctx, session, task.ID, spec)
	if err != nil {
		if managed {
			cleanupContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			_ = handler.bindings.Delete(cleanupContext, session.Namespace, task.ID)
			cancel()
		}
		now := handler.now().UTC()
		_ = handler.storage.Tasks().UpdateState(ctx, task.ID, remotetask.Pending, remotetask.Failed, task.Result, now)
		task.State, task.UpdatedAt = remotetask.Failed, now
		return &controller.APIError{Code: controller.CodeUnavailable, Message: "Kubernetes Port Forward binding failed", Cause: err}
	}
	now := handler.now().UTC()
	if err := handler.storage.Tasks().UpdateState(
		ctx, task.ID, remotetask.Pending, remotetask.Running, task.Result, now,
	); err != nil {
		if errors.Is(err, storage.ErrConflict) {
			current, getErr := handler.storage.Tasks().GetByID(ctx, task.ID)
			if getErr == nil && current.State == remotetask.Running {
				*task = current
				return nil
			}
		}
		return mapStorageError(err)
	}
	task.State, task.UpdatedAt = remotetask.Running, now
	return nil
}

func normalizeSpec(spec *Spec) *controller.APIError {
	spec.Kind = strings.ToLower(strings.TrimSpace(spec.Kind))
	spec.Name = strings.TrimSpace(spec.Name)
	spec.Protocol = strings.ToLower(strings.TrimSpace(spec.Protocol))
	if spec.Kind != "pod" && spec.Kind != "service" {
		return &controller.APIError{Code: controller.CodeInvalidArgument, Field: "kind", Message: "kind must be pod or service"}
	}
	if len(validation.IsDNS1123Subdomain(spec.Name)) != 0 {
		return &controller.APIError{Code: controller.CodeInvalidArgument, Field: "name", Message: "target name is invalid"}
	}
	if spec.Protocol == "" {
		spec.Protocol = "tcp"
	}
	if spec.Protocol != "tcp" && spec.Protocol != "udp" {
		return &controller.APIError{Code: controller.CodeInvalidArgument, Field: "protocol", Message: "protocol must be tcp or udp"}
	}
	if spec.RemotePort == 0 {
		return &controller.APIError{Code: controller.CodeInvalidArgument, Field: "remotePort", Message: "remotePort is required"}
	}
	return nil
}

func validateTarget(target Target) error {
	if strings.TrimSpace(target.Host) != target.Host || target.Host == "" || target.Port == 0 || net.ParseIP(target.Host) == nil {
		return errors.New("resolved target must contain an IP address and port")
	}
	return nil
}

func documentFromTask(task storage.Task, namespace string) Document {
	document, _ := decodeTask(task, namespace)
	return document
}

func decodeTask(task storage.Task, namespace string) (Document, error) {
	var spec Spec
	var target Target
	if err := json.Unmarshal(task.Spec, &spec); err != nil {
		return Document{}, errors.New("decode Port Forward task spec")
	}
	if err := json.Unmarshal(task.Result, &target); err != nil {
		return Document{}, errors.New("decode Port Forward task target")
	}
	if apiError := normalizeSpec(&spec); apiError != nil {
		return Document{}, errors.New("stored Port Forward task spec is invalid")
	}
	if err := validateTarget(target); err != nil {
		return Document{}, err
	}
	expiresAt := time.Time{}
	if task.ExpiresAt != nil {
		expiresAt = task.ExpiresAt.UTC()
	}
	return Document{
		ID: task.ID, SessionID: task.SessionID, Namespace: namespace, State: task.State,
		Kind: spec.Kind, Name: spec.Name, Protocol: spec.Protocol, RemotePort: spec.RemotePort,
		DialAddress: target.Address(), CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt, ExpiresAt: expiresAt,
	}, nil
}

func routeParts(path string) ([]string, bool) {
	suffix, ok := strings.CutPrefix(path, controller.APIPathPrefix+"/sessions/")
	if !ok || strings.HasSuffix(suffix, "/") || strings.Contains(suffix, "//") {
		return nil, false
	}
	parts := strings.Split(suffix, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] != "port-forwards" {
		return nil, false
	}
	return append([]string{"sessions"}, parts...), true
}

func namespaceFromQuery(request *http.Request) (string, *controller.APIError) {
	query := request.URL.Query()
	for key, values := range query {
		if key != "namespace" || len(values) != 1 {
			return "", &controller.APIError{Code: controller.CodeInvalidArgument, Field: key, Message: "only one namespace query parameter is supported"}
		}
	}
	namespace := query.Get("namespace")
	if len(validation.IsDNS1123Label(namespace)) != 0 {
		return "", &controller.APIError{Code: controller.CodeInvalidArgument, Field: "namespace", Message: "namespace is invalid"}
	}
	return namespace, nil
}

func requireEmptyBody(request *http.Request) *controller.APIError {
	contents, err := io.ReadAll(io.LimitReader(request.Body, 1))
	if err != nil {
		return &controller.APIError{Code: controller.CodeInvalidArgument, Message: "request body is invalid"}
	}
	if len(contents) != 0 {
		return &controller.APIError{Code: controller.CodeInvalidArgument, Message: "request body must be empty"}
	}
	return nil
}

func mapStorageError(err error) *controller.APIError {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return notFound()
	case errors.Is(err, storage.ErrConflict):
		return &controller.APIError{Code: controller.CodeConflict, Message: "Port Forward Task state changed; reload and retry", Cause: err}
	case errors.Is(err, storage.ErrIdempotencyMismatch):
		return &controller.APIError{Code: controller.CodeConflict, Message: "Idempotency-Key was already used for a different request", Cause: err}
	default:
		return internalError(err)
	}
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func internalError(err error) *controller.APIError {
	return &controller.APIError{Code: controller.CodeInternal, Message: "Port Forward Task operation failed", Cause: err}
}

func notFound() *controller.APIError {
	return &controller.APIError{Code: controller.CodeNotFound, Message: "resource not found"}
}
