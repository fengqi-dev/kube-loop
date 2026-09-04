package taskapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	controlplanemiddleware "github.com/fengqi-dev/kube-loop/internal/controlplane/middleware"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

// Storage is what an idempotent task create needs: the repositories, and the
// transaction that makes reserving the key and creating the task one write.
type Storage interface {
	storage.Repositories
	storage.TransactionManager
}

// Creator performs the idempotent creation of one storage-backed task type,
// over that type's own spec S and document D. Pod exec, remote file operations
// and file transfers all create a task the same way: reject a reused
// Idempotency-Key whose request differs, replay the task an identical key
// already created, and otherwise reserve the key and persist the task in one
// transaction.
type Creator[S any, D any] struct {
	TaskType string
	Storage  Storage
	Now      func() time.Time
	Errors   Errors

	// Normalize validates and canonicalizes a bound create request.
	Normalize func(*S) *controlplaneapi.Error
	// Prepare runs after the replay check and before the task is persisted, so
	// the API can resolve and validate its Kubernetes target against the spec
	// it is about to store. It may refine the spec.
	Prepare func(
		context.Context, controlplaneapi.Identity, sessionapi.ActiveSession, *S,
	) *controlplaneapi.Error
	// Document renders a stored task as this API's wire document.
	Document func(storage.Task, string) (D, error)
	// Location builds the Location header value for a created task.
	Location func(sessionapi.ActiveSession, string) string
	// RecordResponse stores the rendered document on the idempotency record.
	// An API whose task is meaningful only through a later stream has nothing
	// worth recording and leaves this false.
	RecordResponse bool
	// AfterCreate optionally performs the task's work before responding, for
	// an operation that completes within the request rather than over a
	// stream. It returns the task as stored afterwards.
	AfterCreate func(
		context.Context, controlplaneapi.Identity, sessionapi.ActiveSession, storage.Task, S,
	) (storage.Task, error)
}

// Create serves one create request end to end.
func (creator Creator[S, D]) Create(
	ctx *echo.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
) *controlplaneapi.Error {
	request := ctx.Request()
	var spec S
	if err := ctx.Bind(&spec); err != nil {
		return controlplanemiddleware.BindingError(err)
	}
	if apiError := creator.Normalize(&spec); apiError != nil {
		return apiError
	}
	key, apiError := IdempotencyKey(request)
	if apiError != nil {
		return apiError
	}
	requestHash, err := RequestHash(session.ID, session.Namespace, spec)
	if err != nil {
		return creator.Errors.Internal(err)
	}
	scope := Scope(creator.TaskType, identity.Subject)
	replayed, found, apiError := creator.replay(
		request.Context(), scope, key, requestHash, identity, session,
	)
	if apiError != nil {
		return apiError
	}
	if found {
		document, err := creator.Document(replayed, session.Namespace)
		if err != nil {
			return creator.Errors.Internal(err)
		}
		ctx.Response().Header().Set("Idempotent-Replayed", "true")
		WriteJSON(ctx, http.StatusOK, document)
		return nil
	}
	if creator.Prepare != nil {
		if apiError := creator.Prepare(request.Context(), identity, session, &spec); apiError != nil {
			return apiError
		}
	}
	task, created, apiError := creator.persist(
		request.Context(), scope, key, requestHash, identity, session, spec,
	)
	if apiError != nil {
		return apiError
	}
	if created && creator.AfterCreate != nil {
		task, err = creator.AfterCreate(request.Context(), identity, session, task, spec)
		if err != nil {
			return creator.Errors.Internal(err)
		}
	}
	document, err := creator.Document(task, session.Namespace)
	if err != nil {
		return creator.Errors.Internal(err)
	}
	ctx.Response().Header().Set("Location", creator.Location(session, task.ID))
	status := http.StatusCreated
	if !created {
		ctx.Response().Header().Set("Idempotent-Replayed", "true")
		status = http.StatusOK
	}
	WriteJSON(ctx, status, document)
	return nil
}

// replay reports the task an identical earlier request already created. A key
// reused for a different request is a conflict; a record pointing at a task the
// caller does not own is reported as absent.
func (creator Creator[S, D]) replay(
	ctx context.Context,
	scope, key, requestHash string,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
) (storage.Task, bool, *controlplaneapi.Error) {
	record, err := creator.Storage.Idempotency().Get(ctx, scope, key)
	if errors.Is(err, storage.ErrNotFound) {
		return storage.Task{}, false, nil
	}
	if err != nil {
		return storage.Task{}, false, creator.Errors.Storage(err)
	}
	if record.RequestHash != requestHash {
		return storage.Task{}, false, creator.Errors.Storage(storage.ErrIdempotencyMismatch)
	}
	task, err := creator.Storage.Tasks().GetByID(ctx, record.ResourceID)
	if err != nil || !Owned(task, creator.TaskType, identity, session) {
		return storage.Task{}, false, controlplaneapi.NotFound()
	}
	return task, true, nil
}

// persist reserves the idempotency key and creates the task in one write. A
// key another request reserved first wins, and this request returns that
// request's task instead of a second one.
func (creator Creator[S, D]) persist(
	ctx context.Context,
	scope, key, requestHash string,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
	spec S,
) (storage.Task, bool, *controlplaneapi.Error) {
	specJSON, err := json.Marshal(spec)
	if err != nil {
		return storage.Task{}, false, creator.Errors.Internal(err)
	}
	now, expiresAt := creator.Now().UTC(), session.ExpiresAt.UTC()
	task := storage.Task{
		ID: uuid.NewString(), IdentityID: identity.Subject, SessionID: session.ID,
		Type: creator.TaskType, State: remotetask.Pending, Spec: specJSON,
		IdempotencyKey: key, CreatedAt: now, UpdatedAt: now, ExpiresAt: &expiresAt,
	}
	var response json.RawMessage
	if creator.RecordResponse {
		// The document of a task that does not exist yet: rendering it can only
		// fail on a spec this request already normalized, so a failure here
		// leaves the record without a response rather than rejecting the write.
		if document, err := creator.Document(task, session.Namespace); err == nil {
			response, _ = json.Marshal(document)
		}
	}
	created := false
	err = creator.Storage.WithinTransaction(ctx, func(repositories storage.Repositories) error {
		record, reserved, err := repositories.Idempotency().Reserve(ctx, storage.IdempotencyRecord{
			Scope: scope, Key: key, RequestHash: requestHash, ResourceType: creator.TaskType,
			ResourceID: task.ID, Response: response, CreatedAt: now, ExpiresAt: expiresAt,
		})
		if err != nil {
			return err
		}
		if !reserved {
			existing, err := repositories.Tasks().GetByID(ctx, record.ResourceID)
			if err != nil || !Owned(existing, creator.TaskType, identity, session) {
				return storage.ErrNotFound
			}
			task = existing
			return nil
		}
		if err := repositories.Tasks().Create(ctx, task); err != nil {
			return err
		}
		created = true
		return nil
	})
	if err != nil {
		return storage.Task{}, false, creator.Errors.Storage(err)
	}
	return task, created, nil
}
