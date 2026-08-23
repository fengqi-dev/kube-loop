package periodic

import (
	"context"
	"time"
)

// Run invokes operation immediately and then once per interval until ctx is
// canceled. The interval starts after each operation completes, so a slow pass
// cannot queue an immediate follow-up pass.
func Run(
	ctx context.Context,
	interval time.Duration,
	operation func(context.Context),
) {
	run(ctx, interval, operation, true)
}

// RunAfter waits for interval before the first invocation, then waits for the
// same interval after each operation completes.
func RunAfter(
	ctx context.Context,
	interval time.Duration,
	operation func(context.Context),
) {
	run(ctx, interval, operation, false)
}

func run(
	ctx context.Context,
	interval time.Duration,
	operation func(context.Context),
	immediate bool,
) {
	if ctx == nil || interval <= 0 || operation == nil {
		return
	}
	if immediate {
		select {
		case <-ctx.Done():
			return
		default:
		}
		operation(ctx)
	}

	timer := time.NewTimer(interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			operation(ctx)
			timer.Reset(interval)
		}
	}
}
