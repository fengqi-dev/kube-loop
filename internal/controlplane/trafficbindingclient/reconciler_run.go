package trafficbindingclient

import (
	"context"
	"errors"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/periodic"
)

func (reconciler *Reconciler) Run(ctx context.Context) {
	periodic.Run(ctx, reconciler.interval, reconciler.runAndLog)
}

func (reconciler *Reconciler) RunOnce(ctx context.Context) (int, error) {
	if ctx == nil {
		return 0, errors.New(
			"traffic binding reconciliation context is required",
		)
	}
	recovered, recoveryErr := reconciler.recoverStaleTasks(ctx)
	removed, orphanErr := reconciler.removeOrphanedBindings(ctx)
	return recovered + removed, errors.Join(recoveryErr, orphanErr)
}

func (reconciler *Reconciler) runAndLog(ctx context.Context) {
	count, err := reconciler.RunOnce(ctx)
	if err != nil {
		if ctx.Err() == nil {
			reconciler.logger.ErrorContext(
				ctx,
				"TrafficBinding reconciliation failed",
				"error",
				err,
			)
		}
		return
	}
	if count > 0 {
		reconciler.logger.InfoContext(
			ctx,
			"Reconciled TrafficBinding ownership",
			"resources",
			count,
		)
	}
}
