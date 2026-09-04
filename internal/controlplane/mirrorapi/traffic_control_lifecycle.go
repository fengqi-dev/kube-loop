package mirrorapi

import (
	"context"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/servicebinding"
)

// release restores the mirrored Service the relay was shadowing.
func (handler *Service) release(ctx context.Context, namespace, taskID string) error {
	return handler.resources.Restore(
		ctx, servicebinding.ServiceInterceptSnapshot{Namespace: namespace}, taskID,
	)
}
