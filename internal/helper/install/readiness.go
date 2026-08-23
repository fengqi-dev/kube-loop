package install

import (
	"context"
	"fmt"
	"time"

	helperprotocol "github.com/fengqi-dev/kube-loop/internal/protocol/helper"
)

func waitForHelperReady(
	ctx context.Context,
	timeout time.Duration,
	interval time.Duration,
	ping func(context.Context) (helperprotocol.Response, error),
) error {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var lastErr error
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		response, err := ping(waitCtx)
		if err == nil && response.Protocol == helperprotocol.Version && response.CoreReady {
			return nil
		}
		switch {
		case err != nil:
			lastErr = err
		case response.Protocol != helperprotocol.Version:
			lastErr = fmt.Errorf(
				"helper protocol %d does not match expected protocol %d",
				response.Protocol,
				helperprotocol.Version,
			)
		default:
			lastErr = fmt.Errorf("helper is running but bundled sing-box is not configured")
		}

		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-waitCtx.Done():
			timer.Stop()
			if err := ctx.Err(); err != nil {
				return err
			}
			if lastErr != nil {
				return fmt.Errorf("helper did not become ready after install: %w", lastErr)
			}
			return fmt.Errorf("helper did not become ready after install")
		case <-timer.C:
		}
	}
}
