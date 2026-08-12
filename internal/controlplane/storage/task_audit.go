package storage

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
	"github.com/google/uuid"
)

const TaskTransitionAuditAction = "task.transition"

type auditRequestIDContextKey struct{}

// WithAuditRequestID carries the API correlation ID into storage-layer events.
// Background workers intentionally omit it and receive a generated ID instead.
func WithAuditRequestID(ctx context.Context, requestID string) context.Context {
	requestID = strings.TrimSpace(requestID)
	if ctx == nil || requestID == "" {
		return ctx
	}
	return context.WithValue(ctx, auditRequestIDContextKey{}, requestID)
}

type auditedTaskRepository struct {
	delegate     TaskRepository
	sessions     SessionRepository
	audit        AuditRepository
	transactions TransactionManager
}

func (repository *auditedTaskRepository) Create(ctx context.Context, task Task) error {
	return repository.delegate.Create(ctx, task)
}

func (repository *auditedTaskRepository) GetByID(ctx context.Context, id string) (Task, error) {
	return repository.delegate.GetByID(ctx, id)
}

func (repository *auditedTaskRepository) List(ctx context.Context, filter TaskListFilter) ([]Task, error) {
	return repository.delegate.List(ctx, filter)
}

func (repository *auditedTaskRepository) ListBySession(ctx context.Context, sessionID string, limit int) ([]Task, error) {
	return repository.delegate.ListBySession(ctx, sessionID, limit)
}

func (repository *auditedTaskRepository) ListStaleByTypeStates(
	ctx context.Context,
	taskType string,
	states []remotetask.State,
	before time.Time,
	limit int,
) ([]Task, error) {
	return repository.delegate.ListStaleByTypeStates(ctx, taskType, states, before, limit)
}

func (repository *auditedTaskRepository) UpdateState(
	ctx context.Context,
	id string,
	expectedState, nextState remotetask.State,
	result json.RawMessage,
	updatedAt time.Time,
) error {
	if repository.transactions != nil {
		return repository.transactions.WithinTransaction(ctx, func(repositories Repositories) error {
			return repositories.Tasks().UpdateState(ctx, id, expectedState, nextState, result, updatedAt)
		})
	}
	if err := repository.delegate.UpdateState(ctx, id, expectedState, nextState, result, updatedAt); err != nil {
		return err
	}
	return repository.appendTransition(ctx, id, expectedState, nextState, updatedAt)
}

func (repository *auditedTaskRepository) ClaimStale(
	ctx context.Context,
	id string,
	expectedState remotetask.State,
	observedUpdatedAt time.Time,
	nextState remotetask.State,
	result json.RawMessage,
	updatedAt time.Time,
) error {
	if repository.transactions != nil {
		return repository.transactions.WithinTransaction(ctx, func(repositories Repositories) error {
			return repositories.Tasks().ClaimStale(
				ctx, id, expectedState, observedUpdatedAt, nextState, result, updatedAt,
			)
		})
	}
	if err := repository.delegate.ClaimStale(
		ctx, id, expectedState, observedUpdatedAt, nextState, result, updatedAt,
	); err != nil {
		return err
	}
	return repository.appendTransition(ctx, id, expectedState, nextState, updatedAt)
}

func (repository *auditedTaskRepository) appendTransition(
	ctx context.Context,
	taskID string,
	previousState, nextState remotetask.State,
	createdAt time.Time,
) error {
	if repository.audit == nil || repository.sessions == nil {
		return errors.New("Task transition audit dependencies are required")
	}
	task, err := repository.delegate.GetByID(ctx, taskID)
	if err != nil {
		return err
	}
	session, err := repository.sessions.GetByID(ctx, task.SessionID)
	if err != nil {
		return err
	}
	requestID, _ := ctx.Value(auditRequestIDContextKey{}).(string)
	source := "api"
	if requestID = strings.TrimSpace(requestID); requestID == "" {
		requestID = "background-" + uuid.NewString()
		source = "background"
	}
	metadata, err := json.Marshal(struct {
		SessionID     string           `json:"sessionId"`
		Namespace     string           `json:"namespace"`
		PreviousState remotetask.State `json:"previousState"`
		NextState     remotetask.State `json:"nextState"`
		Source        string           `json:"source"`
	}{
		SessionID: task.SessionID, Namespace: session.Namespace,
		PreviousState: previousState, NextState: nextState, Source: source,
	})
	if err != nil {
		return errors.New("encode Task transition audit metadata")
	}
	return repository.audit.Append(ctx, AuditEvent{
		ID: uuid.NewString(), PrincipalID: task.PrincipalID, Action: TaskTransitionAuditAction,
		ResourceType: task.Type, ResourceID: task.ID, Outcome: "success",
		RequestID: requestID, Metadata: metadata, CreatedAt: createdAt.UTC(),
	})
}

var _ TaskRepository = (*auditedTaskRepository)(nil)
