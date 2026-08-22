package trafficbindingclient

import (
	"context"
	"errors"
	"time"
)

func (reconciler *Reconciler) Run(ctx context.Context) {
	reconciler.runAndLog(ctx)
	ticker := time.NewTicker(reconciler.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reconciler.runAndLog(ctx)
		}
	}
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
