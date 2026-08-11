package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/helper"
	helperinstall "github.com/fengqi-dev/kube-loop/internal/helper/install"
	"github.com/fengqi-dev/kube-loop/internal/singbox"
	singboxruntime "github.com/fengqi-dev/kube-loop/internal/singbox/runtime"
)

// NewSingboxRuntime connects the Data Plane to the narrowly scoped local
// privileged helper. This is local network plumbing only; it never loads a
// kubeconfig or talks to Kubernetes.
func NewSingboxRuntime(appendLog func(string, string)) *singboxruntime.Runtime {
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
			if !helperSessionBusy(err) {
				return nil, fmt.Errorf("helper start session: %w", err)
			}
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
	runtime.PrivilegedUpdateDNS = func(ctx context.Context, sessionID string, dns singbox.DNSMeta) error {
		client, err := helper.NewClient()
		if err != nil {
			return err
		}
		if _, err := client.UpdateDNS(ctx, sessionID, dns); err != nil {
			return fmt.Errorf("helper update DNS: %w", err)
		}
		return nil
	}
	runtime.PrivilegedReadLogs = func(
		ctx context.Context, sessionID string, offset int64,
	) (string, int64, error) {
		client, err := helper.NewClient()
		if err != nil {
			return "", offset, err
		}
		response, err := client.ReadLogs(ctx, sessionID, offset)
		if err != nil {
			return "", offset, err
		}
		return response.LogData, response.LogOffset, nil
	}
	return runtime
}

func helperSessionBusy(err error) bool {
	return err != nil && strings.Contains(err.Error(), "another privileged TUN session is already active")
}
