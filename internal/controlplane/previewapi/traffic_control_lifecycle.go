package previewapi

import (
	"context"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/servicebinding"
)

// release deletes the Service this Preview published. Unlike Exchange and
// Mirror there is nothing to restore: the Service only ever existed for the
// Preview (ADR 0013).
func (handler *Service) release(ctx context.Context, namespace, taskID string) error {
	return handler.resources.Delete(
		ctx, servicebinding.PreviewServiceSnapshot{Namespace: namespace}, taskID,
	)
}
