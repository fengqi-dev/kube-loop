package fileapi

import (
	"github.com/labstack/echo/v5"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/taskapi"
)

func targetError(err error) *controlplaneapi.Error {
	switch {
	case apierrors.IsForbidden(err):
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeForbidden,
			Message: "Kubernetes file access is not permitted",
			Cause:   err,
		}
	case apierrors.IsNotFound(err):
		return notFound()
	default:
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeInvalidArgument,
			Message: "file transfer target is unavailable",
			Cause:   err,
		}
	}
}

// apiErrors names this API in every message a client can see. The mapping from
// storage failures to those messages is shared with the other task APIs.
var apiErrors = taskapi.Errors{
	Name:     "file transfer",
	Conflict: "file transfer Task conflicts with an existing request",
}

func storageError(err error) *controlplaneapi.Error { return apiErrors.Storage(err) }

func invalid(field, message string) *controlplaneapi.Error {
	return controlplaneapi.Invalid(field, message)
}

func internalError(err error) *controlplaneapi.Error { return apiErrors.Internal(err) }

func notFound() *controlplaneapi.Error { return controlplaneapi.NotFound() }

func writeJSON(ctx *echo.Context, status int, value any) {
	taskapi.WriteJSON(ctx, status, value)
}
