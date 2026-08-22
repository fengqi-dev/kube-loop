package fileopsapi

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v5"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

func namespaceFromQuery(
	request *http.Request,
) (string, *controlplaneapi.Error) {
	query := request.URL.Query()
	if len(query) != 1 || len(query["namespace"]) != 1 ||
		len(validation.IsDNS1123Label(query.Get("namespace"))) != 0 {
		return "", invalid(
			"namespace",
			"one valid namespace query parameter is required",
		)
	}
	return query.Get("namespace"), nil
}

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
	return &controlplaneapi.Error{
		Code:    controlplaneapi.CodeInvalidArgument,
		Field:   field,
		Message: message,
	}
}

func internalError(err error) *controlplaneapi.Error {
	return &controlplaneapi.Error{
		Code:    controlplaneapi.CodeInternal,
		Message: "remote file operation failed",
		Cause:   err,
	}
}

func notFound() *controlplaneapi.Error {
	return &controlplaneapi.Error{
		Code:    controlplaneapi.CodeNotFound,
		Message: "resource not found",
	}
}

func writeJSON(ctx *echo.Context, status int, value any) {
	_ = ctx.JSON(status, value)
}
