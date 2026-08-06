package session

import (
	"context"
	"fmt"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/helper"
	helperinstall "github.com/fengqi-dev/kube-loop/internal/helper/install"
	"github.com/fengqi-dev/kube-loop/internal/singbox"
	singboxruntime "github.com/fengqi-dev/kube-loop/internal/singbox/runtime"
)

func newSingboxRuntime(appendLog func(string, string)) *singboxruntime.Runtime {
	logEvent := func(level, message string) {
		if appendLog != nil {
			appendLog(level, message)
		}
	}
	runtime := &singboxruntime.Runtime{}
	runtime.PrivilegedStart = func(
		ctx context.Context, spec singbox.SessionSpec,
	) (func(context.Context) error, error) {
		logEvent("INFO", "ensuring privileged helper is ready")
		if err := helperinstall.EnsureInstall(ctx); err != nil {
			return nil, fmt.Errorf("ensure privileged helper: %w", err)
		}
		client, err := helper.NewClient()
		if err != nil {
			return nil, err
		}
		if _, err := client.Start(ctx, spec); err != nil {
			if !isHelperSessionBusy(err) {
				return nil, fmt.Errorf("helper start session: %w", err)
			}
			// Crash/reload can leave a privileged TUN behind. Clear it once and retry.
			logEvent("WARN", "leftover privileged TUN session detected; stopping it before retry")
			if _, stopErr := client.StopAll(ctx); stopErr != nil {
				return nil, fmt.Errorf("helper start session: %w (stop-all: %v)", err, stopErr)
			}
			if _, err := client.Start(ctx, spec); err != nil {
				return nil, fmt.Errorf("helper start session: %w", err)
			}
		}
		logEvent("INFO", "privileged TUN session started")
		return func(stopCtx context.Context) error {
			_, err := client.Stop(stopCtx, spec.ID)
			return err
		}, nil
	}
	runtime.PrivilegedUpdateDNS = func(
		ctx context.Context, sessionID string, dns singbox.DNSMeta,
	) error {
		client, err := helper.NewClient()
		if err != nil {
			return err
		}
		if _, err := client.UpdateDNS(ctx, sessionID, dns); err != nil {
			return fmt.Errorf("helper update DNS: %w", err)
		}
		return nil
	}
	return runtime
}

func isHelperSessionBusy(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "another privileged TUN session is already active")
}
