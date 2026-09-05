package fileapi

import (
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
		return controlplaneapi.NotFound()
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
