package execapi

import (
	"errors"
	"math"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

type testExitError int64

func (err testExitError) Error() string   { return "command failed" }
func (err testExitError) ExitStatus() int { return int(err) }

func TestStatusFromErrorPreservesSafeExitCodeAndCancellation(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		cancelled bool
		code      uint32
		message   string
	}{
		{name: "success"},
		{name: "generic failure", err: errors.New("failed"), code: 1, message: "command exited unsuccessfully"},
		{name: "process exit", err: testExitError(42), code: 42, message: "command exited unsuccessfully"},
		{name: "negative exit", err: testExitError(-1), code: 1, message: "command exited unsuccessfully"},
		{
			name: "overflow exit", err: testExitError(int64(math.MaxUint32) + 1),
			code: 1, message: "command exited unsuccessfully",
		},
		{name: "cancelled", err: testExitError(130), cancelled: true, code: 130},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status := statusFromError(test.err, test.cancelled)
			if status.Code != test.code || status.Cancelled != test.cancelled || status.Error != test.message {
				t.Fatalf("exit status = %#v", status)
			}
		})
	}
}

func TestStorageErrorMapsStablePublicCategories(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected controlplaneapi.ErrorCode
	}{
		{name: "not found", err: storage.ErrNotFound, expected: controlplaneapi.CodeNotFound},
		{name: "conflict", err: storage.ErrConflict, expected: controlplaneapi.CodeConflict},
		{name: "idempotency mismatch", err: storage.ErrIdempotencyMismatch, expected: controlplaneapi.CodeConflict},
		{name: "internal", err: errors.New("database offline"), expected: controlplaneapi.CodeInternal},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			apiError := storageError(test.err)
			if apiError.Code != test.expected ||
				(test.expected != controlplaneapi.CodeNotFound && !errors.Is(apiError.Cause, test.err)) {
				t.Fatalf("storage error = %#v", apiError)
			}
		})
	}
}
