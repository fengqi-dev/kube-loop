package taskapi

import (
	"errors"

	"github.com/labstack/echo/v5"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

// Errors builds the API errors for one storage-backed task type. Name is the
// user-facing noun that appears in the messages -- "Pod exec", "remote file",
// "file transfer" -- so a client can tell which API rejected the request.
type Errors struct {
	Name string
	// Conflict overrides the message reported when stored task state changed
	// under the request. Empty means "<Name> Task state changed; reload and
	// retry".
	Conflict string
	// IdempotencyMismatch overrides the message reported when a key was reused
	// for a different request. Empty reports it as an ordinary conflict, which
	// is what an API that does not distinguish the two wants.
	IdempotencyMismatch string
}

// Storage maps a persistence error onto the client-visible categories. Every
// conflict shape collapses onto one conflict unless the API distinguishes a
// reused Idempotency-Key.
func (reporter Errors) Storage(err error) *controlplaneapi.Error {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return controlplaneapi.NotFound()
	case errors.Is(err, storage.ErrIdempotencyMismatch) && reporter.IdempotencyMismatch != "":
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeConflict,
			Message: reporter.IdempotencyMismatch,
			Cause:   err,
		}
	case errors.Is(err, storage.ErrConflict), errors.Is(err, storage.ErrIdempotencyMismatch):
		message := reporter.Conflict
		if message == "" {
			message = reporter.Name + " Task state changed; reload and retry"
		}
		return &controlplaneapi.Error{
			Code: controlplaneapi.CodeConflict, Message: message, Cause: err,
		}
	default:
		return reporter.Internal(err)
	}
}

// Internal reports a failure the client cannot act on.
func (reporter Errors) Internal(err error) *controlplaneapi.Error {
	return &controlplaneapi.Error{
		Code:    controlplaneapi.CodeInternal,
		Message: reporter.Name + " operation failed",
		Cause:   err,
	}
}

// Owned reports whether task is of taskType and belongs to the identity's
// active session. A task that is not owned is reported as absent, never as
// forbidden, so one client cannot probe another's task IDs.
func Owned(
	task storage.Task,
	taskType string,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
) bool {
	return task.Type == taskType && task.IdentityID == identity.Subject &&
		task.SessionID == session.ID
}

func WriteJSON(ctx *echo.Context, status int, value any) {
	_ = ctx.JSON(status, value)
}
