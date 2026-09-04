package helperinstall

import (
	"context"
	"fmt"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/helper"
	"github.com/fengqi-dev/kube-loop/internal/protocol/helperrpc"
)

func waitForInstalledHelper(ctx context.Context, token string) error {
	client := &helper.Client{Token: token}
	return waitForHelperReady(
		ctx,
		20*time.Second,
		100*time.Millisecond,
		func(pingCtx context.Context) (helperrpc.Response, error) {
			requestCtx, cancel := context.WithTimeout(pingCtx, 2*time.Second)
			defer cancel()
			response, err := client.Ping(requestCtx)
			if err == nil && response.Version != helper.Version {
				return response, fmt.Errorf(
					"helper version %q does not match expected version %q",
					response.Version,
					helper.Version,
				)
			}
			return response, err
		},
	)
}

func waitForHelperReady(
	ctx context.Context,
	timeout time.Duration,
	interval time.Duration,
	ping func(context.Context) (helperrpc.Response, error),
) error {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var lastErr error
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		response, err := ping(waitCtx)
		if err == nil && response.Protocol == helperrpc.Version && response.CoreReady {
			return nil
		}
		switch {
		case err != nil:
			lastErr = err
		case response.Protocol != helperrpc.Version:
			lastErr = fmt.Errorf(
				"helper protocol %d does not match expected protocol %d",
				response.Protocol,
				helperrpc.Version,
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
