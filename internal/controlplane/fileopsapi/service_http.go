package fileopsapi

import (
	"errors"

	"github.com/labstack/echo/v5"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

func storageError(err error) *controlplaneapi.Error {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return notFound()
	case errors.Is(err, storage.ErrIdempotencyMismatch):
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeConflict,
			Message: "Idempotency-Key was already used for a different request",
			Cause:   err,
		}
	case errors.Is(err, storage.ErrConflict):
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeConflict,
			Message: "remote file Task state changed; reload and retry",
			Cause:   err,
		}
	default:
		return internalError(err)
	}
}

func targetError(err error) *controlplaneapi.Error {
	return &controlplaneapi.Error{
		Code:    controlplaneapi.CodeInvalidArgument,
		Message: "Pod file target is unavailable",
		Cause:   err,
	}
}

func invalid(field, message string) *controlplaneapi.Error {
	return controlplaneapi.Invalid(field, message)
}

func internalError(err error) *controlplaneapi.Error {
	return &controlplaneapi.Error{
		Code:    controlplaneapi.CodeInternal,
		Message: "remote file operation failed",
		Cause:   err,
	}
}

func notFound() *controlplaneapi.Error { return controlplaneapi.NotFound() }

func writeJSON(ctx *echo.Context, status int, value any) {
	_ = ctx.JSON(status, value)
}
