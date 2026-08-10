package execapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/fengqi-dev/kube-loop/internal/controller"
	"github.com/fengqi-dev/kube-loop/internal/controller/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controller/storage"
	"github.com/fengqi-dev/kube-loop/internal/controller/streamlease"
	"github.com/fengqi-dev/kube-loop/internal/controller/taskapi"
	"github.com/fengqi-dev/kube-loop/internal/protocol/execstream"
	"github.com/fengqi-dev/kube-loop/internal/remotetask"
	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/util/validation"
)

const TaskType = "pod-exec"

type Storage interface {
	storage.Repositories
	storage.TransactionManager
}

type SessionValidator interface {
	RequireActive(context.Context, controller.Principal, string, string) (sessionapi.ActiveSession, *controller.APIError)
}

type Config struct {
	Now                     func() time.Time
	CredentialCheckInterval time.Duration
}

type Handler struct {
	storage                 Storage
	sessions                SessionValidator
	executor                Executor
	now                     func() time.Time
	credentialCheckInterval time.Duration
}

type Spec struct {
	Pod       string   `json:"pod"`
	Container string   `json:"container,omitempty"`
	Command   []string `json:"command"`
	TTY       bool     `json:"tty"`
}

type Document struct {
	ID        string           `json:"id"`
	SessionID string           `json:"sessionId"`
	Namespace string           `json:"namespace"`
	State     remotetask.State `json:"state"`
	Pod       string           `json:"pod"`
	Container string           `json:"container,omitempty"`
	TTY       bool             `json:"tty"`
	CreatedAt time.Time        `json:"createdAt"`
	UpdatedAt time.Time        `json:"updatedAt"`
	ExpiresAt time.Time        `json:"expiresAt"`
}

func New(storageBackend Storage, sessions SessionValidator, executor Executor, config Config) (*Handler, error) {
	if storageBackend == nil || sessions == nil || executor == nil {
		return nil, errors.New("Pod exec storage, Session validator and Kubernetes executor are required")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.CredentialCheckInterval == 0 {
		config.CredentialCheckInterval = 5 * time.Second
	}
	if config.CredentialCheckInterval < 10*time.Millisecond || config.CredentialCheckInterval > 30*time.Second {
		return nil, errors.New("Pod exec credential check interval must be between 10ms and 30s")
	}
	return &Handler{
		storage: storageBackend, sessions: sessions, executor: executor, now: config.Now,
		credentialCheckInterval: config.CredentialCheckInterval,
	}, nil
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
	session, apiError := handler.sessions.RequireActive(request.Context(), principal, namespace, parts[1])
	if apiError != nil {
		return apiError
	}
	controller.SetAuditSessionID(request.Context(), session.ID)
	switch {
	case len(parts) == 3 && request.Method == http.MethodPost:
		return handler.create(writer, request, principal, session)
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
	if apiError := normalizeSpec(&spec); apiError != nil {
		return apiError
	}
	key, apiError := taskapi.IdempotencyKey(request)
	if apiError != nil {
		return apiError
	}
	specJSON, _ := json.Marshal(spec)
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
	if err := handler.executor.Validate(request.Context(), principal, session.Namespace, spec); err != nil {
		return &controller.APIError{Code: controller.CodeInvalidArgument, Message: "Pod exec target is unavailable", Cause: err}
	}
	now := handler.now().UTC()
	expiresAt := session.ExpiresAt.UTC()
	task := storage.Task{
		ID: uuid.NewString(), PrincipalID: principal.Subject, SessionID: session.ID,
		Type: TaskType, State: remotetask.Pending, Spec: specJSON, IdempotencyKey: key,
		CreatedAt: now, UpdatedAt: now, ExpiresAt: &expiresAt,
	}
	document := documentFromTask(task, session.Namespace)
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
			if err != nil {
				return err
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
	writer.Header().Set("Location", fmt.Sprintf("%s/sessions/%s/exec/%s/stream?namespace=%s", controller.APIPathPrefix, session.ID, task.ID, session.Namespace))
	if !created {
		writer.Header().Set("Idempotent-Replayed", "true")
	}
	writeJSON(writer, map[bool]int{true: http.StatusCreated, false: http.StatusOK}[created], document)
	return nil
}

func (handler *Handler) stream(
	writer http.ResponseWriter,
	request *http.Request,
	principal controller.Principal,
	session sessionapi.ActiveSession,
	taskID string,
) *controller.APIError {
	if _, err := uuid.Parse(taskID); err != nil {
		return notFound()
	}
	task, err := handler.storage.Tasks().GetByID(request.Context(), taskID)
	if err != nil || !owned(task, principal, session) {
		return notFound()
	}
	if task.State != remotetask.Pending {
		return &controller.APIError{Code: controller.CodeConflict, Message: "Pod exec Task was already claimed"}
	}
	spec, err := specFromTask(task)
	if err != nil {
		return internalError(err)
	}
	if err := handler.storage.Tasks().UpdateState(request.Context(), task.ID, remotetask.Pending, remotetask.Starting, json.RawMessage(`{}`), handler.now().UTC()); err != nil {
		return storageError(err)
	}
	connection, err := websocket.Accept(writer, request, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		_ = handler.storage.Tasks().UpdateState(request.Context(), task.ID, remotetask.Starting, remotetask.Failed, json.RawMessage(`{"error":"WebSocket upgrade failed"}`), handler.now().UTC())
		return nil
	}
	defer connection.CloseNow()
	connection.SetReadLimit(execstream.MaximumPayload + 1)
	streamContext, cancel, contextErr := streamlease.Start(request.Context(), handler.storage, principal, session, streamlease.Config{
		Now: handler.now, CheckInterval: handler.credentialCheckInterval,
		Runtime: streamlease.RuntimeFrom(handler.sessions), TaskID: task.ID, HeartbeatTask: true,
	})
	if contextErr != nil {
		_ = handler.storage.Tasks().UpdateState(context.Background(), task.ID, remotetask.Starting, remotetask.Failed, json.RawMessage(`{"error":"authorization lease expired"}`), handler.now().UTC())
		_ = connection.Close(websocket.StatusPolicyViolation, "authorization lease expired")
		return nil
	}
	defer cancel()
	if err := handler.storage.Tasks().UpdateState(request.Context(), task.ID, remotetask.Starting, remotetask.Running, json.RawMessage(`{}`), handler.now().UTC()); err != nil {
		_ = connection.Close(websocket.StatusInternalError, "exec state persistence failed")
		return nil
	}
	stdinReader, stdinWriter := io.Pipe()
	defer stdinReader.Close()
	sizes := newTerminalSizeQueue()
	defer sizes.Close()
	go func() {
		inputErr := readInput(request.Context(), connection, stdinWriter, sizes)
		if inputErr != nil {
			cancel()
		}
	}()
	var writeMu sync.Mutex
	streams := Streams{
		Stdin: stdinReader, Stdout: frameWriter{ctx: streamContext, connection: connection, frameType: execstream.Stdout, mu: &writeMu},
		TTY: spec.TTY, TerminalSizeQueue: sizes,
	}
	if !spec.TTY {
		streams.Stderr = frameWriter{ctx: streamContext, connection: connection, frameType: execstream.Stderr, mu: &writeMu}
	}
	execErr := handler.executor.Exec(streamContext, principal, session.Namespace, spec, streams)
	cancelled := streamContext.Err() != nil
	_ = stdinWriter.Close()
	exitStatus := statusFromError(execErr, cancelled)
	nextState := remotetask.Stopped
	if execErr != nil && !cancelled {
		nextState = remotetask.Failed
	}
	persistContext, persistCancel := context.WithTimeout(context.Background(), 5*time.Second)
	persistErr := handler.storage.Tasks().UpdateState(persistContext, task.ID, remotetask.Running, nextState, taskResult(exitStatus), handler.now().UTC())
	persistCancel()
	if persistErr != nil {
		cancel()
		_ = connection.Close(websocket.StatusInternalError, "exec state persistence failed")
		return nil
	}
	encoded, _ := execstream.EncodeExit(exitStatus)
	writeMu.Lock()
	_ = connection.Write(context.Background(), websocket.MessageBinary, encoded)
	writeMu.Unlock()
	cancel()
	_ = connection.Close(websocket.StatusNormalClosure, "exec complete")
	return nil
}

func statusFromError(err error, cancelled bool) execstream.ExitStatus {
	status := execstream.ExitStatus{Cancelled: cancelled}
	if err == nil {
		return status
	}
	status.Code = 1
	var exitError interface{ ExitStatus() int }
	if errors.As(err, &exitError) && exitError.ExitStatus() >= 0 {
		status.Code = uint32(exitError.ExitStatus())
	}
	if !cancelled {
		status.Error = "command exited unsuccessfully"
	}
	return status
}

func normalizeSpec(spec *Spec) *controller.APIError {
	spec.Pod = strings.TrimSpace(spec.Pod)
	spec.Container = strings.TrimSpace(spec.Container)
	if len(validation.IsDNS1123Subdomain(spec.Pod)) != 0 {
		return &controller.APIError{Code: controller.CodeInvalidArgument, Field: "pod", Message: "Pod name is invalid"}
	}
	if spec.Container != "" && len(validation.IsDNS1123Label(spec.Container)) != 0 {
		return &controller.APIError{Code: controller.CodeInvalidArgument, Field: "container", Message: "container name is invalid"}
	}
	if len(spec.Command) == 0 || len(spec.Command) > 64 {
		return &controller.APIError{Code: controller.CodeInvalidArgument, Field: "command", Message: "command must contain 1 to 64 arguments"}
	}
	total := 0
	for index, argument := range spec.Command {
		if argument == "" || len(argument) > 4096 || strings.IndexByte(argument, 0) >= 0 {
			return &controller.APIError{Code: controller.CodeInvalidArgument, Field: fmt.Sprintf("command[%d]", index), Message: "command argument is invalid"}
		}
		total += len(argument)
	}
	if total > 16<<10 {
		return &controller.APIError{Code: controller.CodeInvalidArgument, Field: "command", Message: "command exceeds 16 KiB"}
	}
	return nil
}

func owned(task storage.Task, principal controller.Principal, session sessionapi.ActiveSession) bool {
	return task.Type == TaskType && task.PrincipalID == principal.Subject && task.SessionID == session.ID
}

func specFromTask(task storage.Task) (Spec, error) {
	var spec Spec
	if err := json.Unmarshal(task.Spec, &spec); err != nil {
		return Spec{}, errors.New("decode Pod exec Task")
	}
	if apiError := normalizeSpec(&spec); apiError != nil {
		return Spec{}, errors.New("stored Pod exec Task is invalid")
	}
	return spec, nil
}

func documentFromTask(task storage.Task, namespace string) Document {
	document, _ := decodeTask(task, namespace)
	return document
}

func decodeTask(task storage.Task, namespace string) (Document, error) {
	spec, err := specFromTask(task)
	if err != nil {
		return Document{}, err
	}
	expiresAt := time.Time{}
	if task.ExpiresAt != nil {
		expiresAt = *task.ExpiresAt
	}
	return Document{ID: task.ID, SessionID: task.SessionID, Namespace: namespace, State: task.State, Pod: spec.Pod, Container: spec.Container, TTY: spec.TTY, CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt, ExpiresAt: expiresAt}, nil
}

func routeParts(path string) ([]string, bool) {
	suffix, ok := strings.CutPrefix(path, controller.APIPathPrefix+"/sessions/")
	if !ok || strings.HasSuffix(suffix, "/") || strings.Contains(suffix, "//") {
		return nil, false
	}
	parts := strings.Split(suffix, "/")
	if len(parts) < 2 || parts[1] != "exec" {
		return nil, false
	}
	return append([]string{"sessions"}, parts...), true
}

func namespaceFromQuery(request *http.Request) (string, *controller.APIError) {
	query := request.URL.Query()
	if len(query) != 1 || len(query["namespace"]) != 1 || len(validation.IsDNS1123Label(query.Get("namespace"))) != 0 {
		return "", &controller.APIError{Code: controller.CodeInvalidArgument, Field: "namespace", Message: "one valid namespace query parameter is required"}
	}
	return query.Get("namespace"), nil
}

func storageError(err error) *controller.APIError {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return notFound()
	case errors.Is(err, storage.ErrConflict), errors.Is(err, storage.ErrIdempotencyMismatch):
		return &controller.APIError{Code: controller.CodeConflict, Message: "Pod exec Task state changed; reload and retry", Cause: err}
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
	return &controller.APIError{Code: controller.CodeInternal, Message: "Pod exec operation failed", Cause: err}
}

func notFound() *controller.APIError {
	return &controller.APIError{Code: controller.CodeNotFound, Message: "resource not found"}
}
