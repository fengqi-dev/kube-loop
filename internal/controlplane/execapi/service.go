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
	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	controlplanemiddleware "github.com/fengqi-dev/kube-loop/internal/controlplane/middleware"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/streamlease"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/taskapi"
	"github.com/fengqi-dev/kube-loop/internal/protocol/execstream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
	"k8s.io/apimachinery/pkg/util/validation"
)

const TaskType = "pod-exec"

type Storage interface {
	storage.Repositories
	storage.TransactionManager
}

type SessionValidator interface {
	RequireActive(context.Context, controlplaneapi.Principal, string, string) (sessionapi.ActiveSession, *controlplaneapi.Error)
}

type Config struct {
	Now                     func() time.Time
	CredentialCheckInterval time.Duration
	Authorizer              authorization.Authorizer
}

type Service struct {
	storage                 Storage
	sessions                SessionValidator
	executor                Executor
	now                     func() time.Time
	credentialCheckInterval time.Duration
	authorizer              authorization.Authorizer
}

func New(storageBackend Storage, sessions SessionValidator, executor Executor, config Config) (*Service, error) {
	if storageBackend == nil || sessions == nil || executor == nil {
		return nil, errors.New("Pod exec storage, Session validator and Kubernetes executor are required")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.CredentialCheckInterval == 0 {
		config.CredentialCheckInterval = 500 * time.Millisecond
	}
	if config.CredentialCheckInterval < 10*time.Millisecond || config.CredentialCheckInterval > 30*time.Second {
		return nil, errors.New("Pod exec credential check interval must be between 10ms and 30s")
	}
	return &Service{
		storage: storageBackend, sessions: sessions, executor: executor, now: config.Now,
		credentialCheckInterval: config.CredentialCheckInterval,
		authorizer:              config.Authorizer,
	}, nil
}

func (handler *Service) create(
	ctx *echo.Context,
	principal controlplaneapi.Principal,
	session sessionapi.ActiveSession,
) *controlplaneapi.Error {
	request := ctx.Request()
	var spec Spec
	if err := ctx.Bind(&spec); err != nil {
		return controlplanemiddleware.BindingError(err)
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
		ctx.Response().Header().Set("Idempotent-Replayed", "true")
		writeJSON(ctx, http.StatusOK, document)
		return nil
	} else if !errors.Is(err, storage.ErrNotFound) {
		return storageError(err)
	}
	if err := handler.executor.Validate(request.Context(), principal, session.Namespace, spec); err != nil {
		return &controlplaneapi.Error{Code: controlplaneapi.CodeInvalidArgument, Message: "Pod exec target is unavailable", Cause: err}
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
	ctx.Response().Header().Set("Location", fmt.Sprintf("%s/sessions/%s/exec/%s/stream?namespace=%s", controlplane.SessionAPIPathPrefix, session.ID, task.ID, session.Namespace))
	if !created {
		ctx.Response().Header().Set("Idempotent-Replayed", "true")
	}
	writeJSON(ctx, map[bool]int{true: http.StatusCreated, false: http.StatusOK}[created], document)
	return nil
}

func (handler *Service) stream(
	ctx *echo.Context,
	principal controlplaneapi.Principal,
	session sessionapi.ActiveSession,
	taskID string,
) *controlplaneapi.Error {
	request := ctx.Request()
	writer := ctx.Response()
	if _, err := uuid.Parse(taskID); err != nil {
		return notFound()
	}
	task, err := handler.storage.Tasks().GetByID(request.Context(), taskID)
	if err != nil || !owned(task, principal, session) {
		return notFound()
	}
	if task.State != remotetask.Pending {
		return &controlplaneapi.Error{Code: controlplaneapi.CodeConflict, Message: "Pod exec Task was already claimed"}
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
		Authorizer: handler.authorizer, Authorization: authorization.Request{
			Operation: "stream", Namespace: session.Namespace, ResourceKind: "pod-exec", ResourceName: task.ID,
		},
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

func normalizeSpec(spec *Spec) *controlplaneapi.Error {
	spec.Pod = strings.TrimSpace(spec.Pod)
	spec.Container = strings.TrimSpace(spec.Container)
	if len(validation.IsDNS1123Subdomain(spec.Pod)) != 0 {
		return &controlplaneapi.Error{Code: controlplaneapi.CodeInvalidArgument, Field: "pod", Message: "Pod name is invalid"}
	}
	if spec.Container != "" && len(validation.IsDNS1123Label(spec.Container)) != 0 {
		return &controlplaneapi.Error{Code: controlplaneapi.CodeInvalidArgument, Field: "container", Message: "container name is invalid"}
	}
	if len(spec.Command) == 0 || len(spec.Command) > 64 {
		return &controlplaneapi.Error{Code: controlplaneapi.CodeInvalidArgument, Field: "command", Message: "command must contain 1 to 64 arguments"}
	}
	total := 0
	for index, argument := range spec.Command {
		if argument == "" || len(argument) > 4096 || strings.IndexByte(argument, 0) >= 0 {
			return &controlplaneapi.Error{Code: controlplaneapi.CodeInvalidArgument, Field: fmt.Sprintf("command[%d]", index), Message: "command argument is invalid"}
		}
		total += len(argument)
	}
	if total > 16<<10 {
		return &controlplaneapi.Error{Code: controlplaneapi.CodeInvalidArgument, Field: "command", Message: "command exceeds 16 KiB"}
	}
	return nil
}

func owned(task storage.Task, principal controlplaneapi.Principal, session sessionapi.ActiveSession) bool {
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

func namespaceFromQuery(request *http.Request) (string, *controlplaneapi.Error) {
	query := request.URL.Query()
	if len(query) != 1 || len(query["namespace"]) != 1 || len(validation.IsDNS1123Label(query.Get("namespace"))) != 0 {
		return "", &controlplaneapi.Error{Code: controlplaneapi.CodeInvalidArgument, Field: "namespace", Message: "one valid namespace query parameter is required"}
	}
	return query.Get("namespace"), nil
}

func storageError(err error) *controlplaneapi.Error {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return notFound()
	case errors.Is(err, storage.ErrConflict), errors.Is(err, storage.ErrIdempotencyMismatch):
		return &controlplaneapi.Error{Code: controlplaneapi.CodeConflict, Message: "Pod exec Task state changed; reload and retry", Cause: err}
	default:
		return internalError(err)
	}
}

func writeJSON(ctx *echo.Context, status int, value any) {
	_ = ctx.JSON(status, value)
}

func internalError(err error) *controlplaneapi.Error {
	return &controlplaneapi.Error{Code: controlplaneapi.CodeInternal, Message: "Pod exec operation failed", Cause: err}
}

func notFound() *controlplaneapi.Error {
	return &controlplaneapi.Error{Code: controlplaneapi.CodeNotFound, Message: "resource not found"}
}
