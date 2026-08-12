package service

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/taskapi"
	"github.com/fengqi-dev/kube-loop/internal/remotetask"
	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/util/validation"
)

const TaskType = "port-forward"

type Storage interface {
	storage.Repositories
	storage.TransactionManager
}

type Resolver interface {
	Resolve(context.Context, controlplaneapi.Principal, string, Spec) (Target, error)
}

type BindingManager interface {
	Activate(context.Context, sessionapi.ActiveSession, string, Spec) (bool, error)
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

func New(storageBackend Storage, resolver Resolver, bindings BindingManager, config Config) (*Service, error) {
	if storageBackend == nil || resolver == nil || bindings == nil {
		return nil, errors.New("Port Forward storage, target resolver and TrafficBinding manager are required")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Service{storage: storageBackend, resolver: resolver, bindings: bindings, now: config.Now}, nil
}

func (service *Service) Create(
	ctx context.Context,
	principal controlplaneapi.Principal,
	session sessionapi.ActiveSession,
	spec Spec,
	idempotencyKey string,
) (CreateResult, *controlplaneapi.Error) {
	if apiError := normalizeSpec(&spec); apiError != nil {
		return CreateResult{}, apiError
	}
	hashSource, _ := json.Marshal(struct {
		SessionID string `json:"sessionId"`
		Namespace string `json:"namespace"`
		Spec      Spec   `json:"spec"`
	}{SessionID: session.ID, Namespace: session.Namespace, Spec: spec})
	legacyRequestHash := taskapi.Hash(hashSource)
	requestHash, err := taskapi.RequestHash(session.ID, session.Namespace, spec)
	if err != nil {
		return CreateResult{}, internalError(err)
	}
	scope := taskapi.Scope(TaskType, principal.Subject)
	if record, getErr := service.storage.Idempotency().Get(ctx, scope, idempotencyKey); getErr == nil {
		if !taskapi.Matches(record.RequestHash, requestHash, legacyRequestHash) {
			return CreateResult{}, mapStorageError(storage.ErrIdempotencyMismatch)
		}
		if record.ResourceType != TaskType {
			return CreateResult{}, mapStorageError(storage.ErrConflict)
		}
		existing, taskErr := service.storage.Tasks().GetByID(ctx, record.ResourceID)
		if taskErr != nil {
			return CreateResult{}, mapStorageError(taskErr)
		}
		if !owned(existing, principal, session) {
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
	target, err := service.resolver.Resolve(ctx, principal, session.Namespace, spec)
	if err != nil {
		return CreateResult{}, &controlplaneapi.Error{Code: controlplaneapi.CodeUnavailable, Message: "Kubernetes Port Forward target resolution failed", Cause: err}
	}
	if err := validateTarget(target); err != nil {
		return CreateResult{}, &controlplaneapi.Error{Code: controlplaneapi.CodeInternal, Message: "Port Forward resolver returned an invalid target", Cause: err}
	}
	now := service.now().UTC()
	specJSON, _ := json.Marshal(spec)
	targetJSON, _ := json.Marshal(target)
	expiresAt := session.ExpiresAt.UTC()
	task := storage.Task{
		ID: uuid.NewString(), PrincipalID: principal.Subject, SessionID: session.ID,
		Type: TaskType, State: remotetask.Pending, Spec: specJSON, Result: targetJSON,
		IdempotencyKey: idempotencyKey, CreatedAt: now, UpdatedAt: now, ExpiresAt: &expiresAt,
	}
	portForward := portForwardFromTask(task, session.Namespace)
	responseJSON, _ := json.Marshal(portForward)
	created := false
	err = service.storage.WithinTransaction(ctx, func(repositories storage.Repositories) error {
		record, reserved, reserveErr := repositories.Idempotency().Reserve(ctx, storage.IdempotencyRecord{
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
			existing, getErr := repositories.Tasks().GetByID(ctx, record.ResourceID)
			if getErr != nil {
				return getErr
			}
			if !owned(existing, principal, session) {
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
	})
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
	return CreateResult{PortForward: portForward, Created: created, Replayed: !created}, nil
}

func (service *Service) List(
	ctx context.Context,
	principal controlplaneapi.Principal,
	session sessionapi.ActiveSession,
) ([]PortForward, *controlplaneapi.Error) {
	tasks, err := service.storage.Tasks().ListBySession(ctx, session.ID, 1000)
	if err != nil {
		return nil, mapStorageError(err)
	}
	items := make([]PortForward, 0, len(tasks))
	for _, task := range tasks {
		if task.Type != TaskType || task.PrincipalID != principal.Subject {
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
	principal controlplaneapi.Principal,
	session sessionapi.ActiveSession,
	taskID string,
) (PortForward, *controlplaneapi.Error) {
	if _, err := uuid.Parse(taskID); err != nil {
		return PortForward{}, notFound()
	}
	task, err := service.storage.Tasks().GetByID(ctx, taskID)
	if err != nil || !owned(task, principal, session) {
		if err != nil && !errors.Is(err, storage.ErrNotFound) {
			return PortForward{}, mapStorageError(err)
		}
		return PortForward{}, notFound()
	}
	if err := service.bindings.Delete(ctx, session.Namespace, task.ID); err != nil {
		return PortForward{}, &controlplaneapi.Error{Code: controlplaneapi.CodeUnavailable, Message: "Port Forward cleanup is pending", Cause: err}
	}
	if !task.State.Terminal() {
		if err := service.storage.Tasks().UpdateState(ctx, task.ID, task.State, remotetask.Stopped, task.Result, service.now().UTC()); err != nil {
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

func (service *Service) activate(ctx context.Context, session sessionapi.ActiveSession, task *storage.Task) *controlplaneapi.Error {
	if task.State != remotetask.Pending {
		return nil
	}
	var spec Spec
	if err := json.Unmarshal(task.Spec, &spec); err != nil {
		return internalError(err)
	}
	managed, err := service.bindings.Activate(ctx, session, task.ID, spec)
	if err != nil {
		if managed {
			cleanupContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			_ = service.bindings.Delete(cleanupContext, session.Namespace, task.ID)
			cancel()
		}
		now := service.now().UTC()
		_ = service.storage.Tasks().UpdateState(ctx, task.ID, remotetask.Pending, remotetask.Failed, task.Result, now)
		task.State, task.UpdatedAt = remotetask.Failed, now
		return &controlplaneapi.Error{Code: controlplaneapi.CodeUnavailable, Message: "Kubernetes Port Forward binding failed", Cause: err}
	}
	now := service.now().UTC()
	if err := service.storage.Tasks().UpdateState(ctx, task.ID, remotetask.Pending, remotetask.Running, task.Result, now); err != nil {
		if errors.Is(err, storage.ErrConflict) {
			current, getErr := service.storage.Tasks().GetByID(ctx, task.ID)
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

func normalizeSpec(spec *Spec) *controlplaneapi.Error {
	spec.Kind = strings.ToLower(strings.TrimSpace(spec.Kind))
	spec.Name = strings.TrimSpace(spec.Name)
	spec.Protocol = strings.ToLower(strings.TrimSpace(spec.Protocol))
	if spec.Kind != "pod" && spec.Kind != "service" {
		return &controlplaneapi.Error{Code: controlplaneapi.CodeInvalidArgument, Field: "kind", Message: "kind must be pod or service"}
	}
	if len(validation.IsDNS1123Subdomain(spec.Name)) != 0 {
		return &controlplaneapi.Error{Code: controlplaneapi.CodeInvalidArgument, Field: "name", Message: "target name is invalid"}
	}
	if spec.Protocol == "" {
		spec.Protocol = "tcp"
	}
	if spec.Protocol != "tcp" && spec.Protocol != "udp" {
		return &controlplaneapi.Error{Code: controlplaneapi.CodeInvalidArgument, Field: "protocol", Message: "protocol must be tcp or udp"}
	}
	if spec.RemotePort == 0 {
		return &controlplaneapi.Error{Code: controlplaneapi.CodeInvalidArgument, Field: "remotePort", Message: "remotePort is required"}
	}
	return nil
}

func validateTarget(target Target) error {
	if strings.TrimSpace(target.Host) != target.Host || target.Host == "" || target.Port == 0 || net.ParseIP(target.Host) == nil {
		return errors.New("resolved target must contain an IP address and port")
	}
	return nil
}

func owned(task storage.Task, principal controlplaneapi.Principal, session sessionapi.ActiveSession) bool {
	return task.Type == TaskType && task.PrincipalID == principal.Subject && task.SessionID == session.ID
}

func portForwardFromTask(task storage.Task, namespace string) PortForward {
	portForward, _ := decodeTask(task, namespace)
	return portForward
}

func decodeTask(task storage.Task, namespace string) (PortForward, error) {
	var spec Spec
	var target Target
	if err := json.Unmarshal(task.Spec, &spec); err != nil {
		return PortForward{}, errors.New("decode Port Forward task spec")
	}
	if err := json.Unmarshal(task.Result, &target); err != nil {
		return PortForward{}, errors.New("decode Port Forward task target")
	}
	if apiError := normalizeSpec(&spec); apiError != nil {
		return PortForward{}, errors.New("stored Port Forward task spec is invalid")
	}
	if err := validateTarget(target); err != nil {
		return PortForward{}, err
	}
	expiresAt := time.Time{}
	if task.ExpiresAt != nil {
		expiresAt = task.ExpiresAt.UTC()
	}
	return PortForward{
		ID: task.ID, SessionID: task.SessionID, Namespace: namespace, State: task.State,
		Kind: spec.Kind, Name: spec.Name, Protocol: spec.Protocol, RemotePort: spec.RemotePort,
		DialAddress: target.Address(), CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt, ExpiresAt: expiresAt,
	}, nil
}

func mapStorageError(err error) *controlplaneapi.Error {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return notFound()
	case errors.Is(err, storage.ErrConflict):
		return &controlplaneapi.Error{Code: controlplaneapi.CodeConflict, Message: "Port Forward Task state changed; reload and retry", Cause: err}
	case errors.Is(err, storage.ErrIdempotencyMismatch):
		return &controlplaneapi.Error{Code: controlplaneapi.CodeConflict, Message: "Idempotency-Key was already used for a different request", Cause: err}
	default:
		return internalError(err)
	}
}

func internalError(err error) *controlplaneapi.Error {
	return &controlplaneapi.Error{Code: controlplaneapi.CodeInternal, Message: "Port Forward Task operation failed", Cause: err}
}

func notFound() *controlplaneapi.Error {
	return &controlplaneapi.Error{Code: controlplaneapi.CodeNotFound, Message: "resource not found"}
}
