package fileopsapi

import (
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/taskapi"
)

// apiErrors names this API in every message a client can see. A reused
// Idempotency-Key is reported separately here, because a client that batches
// file operations needs to tell it apart from ordinary state drift.
var apiErrors = taskapi.Errors{
	Name:                "remote file",
	IdempotencyMismatch: "Idempotency-Key was already used for a different request",
}

func targetError(err error) *controlplaneapi.Error {
	return &controlplaneapi.Error{
		Code:    controlplaneapi.CodeInvalidArgument,
		Message: "Pod file target is unavailable",
		Cause:   err,
	}
}
