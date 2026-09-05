package fileopsapi

import (
	"errors"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

func TestStorageErrorMapsStablePublicCategories(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected controlplaneapi.ErrorCode
	}{
		{name: "not found", err: storage.ErrNotFound, expected: controlplaneapi.CodeNotFound},
		{name: "idempotency mismatch", err: storage.ErrIdempotencyMismatch, expected: controlplaneapi.CodeConflict},
		{name: "conflict", err: storage.ErrConflict, expected: controlplaneapi.CodeConflict},
		{name: "internal", err: errors.New("database offline"), expected: controlplaneapi.CodeInternal},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			apiError := apiErrors.Storage(test.err)
			if apiError.Code != test.expected ||
				(test.expected != controlplaneapi.CodeNotFound && !errors.Is(apiError.Cause, test.err)) {
				t.Fatalf("storage error = %#v", apiError)
			}
		})
	}
}

func TestTargetErrorPreservesCauseAsInvalidArgument(t *testing.T) {
	cause := errors.New("container is unavailable")
	apiError := targetError(cause)
	if apiError.Code != controlplaneapi.CodeInvalidArgument || !errors.Is(apiError.Cause, cause) {
		t.Fatalf("target error = %#v", apiError)
	}
}
