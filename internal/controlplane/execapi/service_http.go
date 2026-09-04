package execapi

import (
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/taskapi"
)

// apiErrors names this API in every message a client can see. The mapping from
// storage failures to those messages is shared with the other task APIs.
var apiErrors = taskapi.Errors{Name: "Pod exec"}

func storageError(err error) *controlplaneapi.Error { return apiErrors.Storage(err) }

func internalError(err error) *controlplaneapi.Error { return apiErrors.Internal(err) }

func notFound() *controlplaneapi.Error { return controlplaneapi.NotFound() }
