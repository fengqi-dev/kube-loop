package exchangeapi

import (
	"context"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/servicebinding"
)

// release restores the intercepted Service the relay was carrying traffic for.
func (handler *Service) release(ctx context.Context, namespace, taskID string) error {
	return handler.resources.Restore(
		ctx, servicebinding.ServiceInterceptSnapshot{Namespace: namespace}, taskID,
	)
}
