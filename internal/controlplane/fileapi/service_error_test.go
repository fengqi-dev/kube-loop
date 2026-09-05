package fileapi

import (
	"context"
	"errors"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

func TestTransferCancellationWaitsForPendingLeaseRevocation(t *testing.T) {
	leaseContext, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	if !transferWasCancelled(
		leaseContext,
		errors.New("partial upload"),
		100*time.Millisecond,
	) {
		t.Fatal("transfer failure was not classified as a pending lease cancellation")
	}
}

func TestTransferCancellationPreservesExecutorFailure(t *testing.T) {
	if transferWasCancelled(
		context.Background(),
		errors.New("executor failed"),
		10*time.Millisecond,
	) {
		t.Fatal("executor failure was classified as a lease cancellation")
	}
}

func TestTargetErrorMapsStablePublicCategories(t *testing.T) {
	resource := schema.GroupResource{Resource: "pods"}
	tests := []struct {
		name     string
		err      error
		expected controlplaneapi.ErrorCode
	}{
		{
			name: "forbidden",
			err: apierrors.NewForbidden(
				resource,
				"api-0",
				errors.New("denied"),
			),
			expected: controlplaneapi.CodeForbidden,
		},
		{
			name:     "not found",
			err:      apierrors.NewNotFound(resource, "api-0"),
			expected: controlplaneapi.CodeNotFound,
		},
		{
			name:     "invalid target",
			err:      errors.New("unavailable"),
			expected: controlplaneapi.CodeInvalidArgument,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			apiError := targetError(test.err)
			if apiError.Code != test.expected ||
				(test.expected != controlplaneapi.CodeNotFound && !errors.Is(apiError.Cause, test.err)) {
				t.Fatalf("target error = %#v", apiError)
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
		{
			name:     "idempotency mismatch",
			err:      storage.ErrIdempotencyMismatch,
			expected: controlplaneapi.CodeConflict,
		},
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
