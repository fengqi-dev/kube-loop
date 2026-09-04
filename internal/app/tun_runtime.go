package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/helper"
	"github.com/fengqi-dev/kube-loop/internal/helperinstall"
	"github.com/fengqi-dev/kube-loop/internal/protocol/sessionspec"
	singboxruntime "github.com/fengqi-dev/kube-loop/internal/singbox/runtime"
)

// cleanupPrivilegedTUNSessions removes sing-box sessions left behind when a
// previous desktop process exited before its normal shutdown hooks ran. A
// stale TUN must not keep intercepting traffic after its user-space SOCKS
// bridge has disappeared.
func cleanupPrivilegedTUNSessions(ctx context.Context) error {
	tokenPath, err := helper.TokenPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(tokenPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("inspect helper token: %w", err)
	}
	client, err := helper.NewClient()
	if err != nil {
		return fmt.Errorf("create helper client: %w", err)
	}
	if _, err := client.StopAll(ctx); err != nil {
		return fmt.Errorf("stop stale privileged TUN sessions: %w", err)
	}
	return nil
}

// NewSingboxRuntime connects the Data Plane to the narrowly scoped local
// privileged helper. This is local network plumbing only; it never loads a
// kubeconfig or talks to Kubernetes. The runtime logger is a child of the
// application logger tagged with component=singbox, so its milestones share
// the same threshold and file sink.
func NewSingboxRuntime(logger *slog.Logger, logLevel string) *singboxruntime.Runtime {
	if logger == nil {
		logger = slog.Default()
	}
	runtime := &singboxruntime.Runtime{LogLevel: logLevel}
	runtime.Logger = logger.With("component", "singbox")
	info := runtime.Logger.Info
	warn := runtime.Logger.Warn
	errLog := runtime.Logger.Error
	runtime.PrivilegedStart = func(
		ctx context.Context, spec sessionspec.Spec,
	) (func(context.Context) error, error) {
		info("ensuring privileged helper is ready")
		if err := helperinstall.EnsureInstall(ctx); err != nil {
			errLog("ensure privileged helper: " + err.Error())
			return nil, fmt.Errorf("ensure privileged helper: %w", err)
		}
		client, err := helper.NewClient()
		if err != nil {
			errLog("helper client: " + err.Error())
			return nil, err
		}
		status, statusErr := client.Status(ctx)
		info(
			"helper status: ok=" + strconv.FormatBool(
				status.OK,
			) + " coreReady=" + strconv.FormatBool(
				status.CoreReady,
			) + " err=" + fmt.Sprint(
				statusErr,
			),
		)
		if _, err := client.Start(ctx, spec); err != nil {
			if !helperSessionBusy(err) {
				errLog("helper start session: " + err.Error())
				return nil, fmt.Errorf("helper start session: %w", err)
			}
			warn("leftover privileged TUN session detected; stopping it before retry")
			if _, stopErr := client.StopAll(ctx); stopErr != nil {
				return nil, fmt.Errorf("helper start session: %w (stop-all: %w)", err, stopErr)
			}
			if _, err := client.Start(ctx, spec); err != nil {
				errLog("helper start session retry: " + err.Error())
				return nil, fmt.Errorf("helper start session: %w", err)
			}
		}
		info("privileged TUN session started")
		return func(stopCtx context.Context) error {
			_, err := client.Stop(stopCtx, spec.ID)
			return err
		}, nil
	}
	runtime.PrivilegedUpdateDNS = func(ctx context.Context, sessionID string, dns sessionspec.DNSMeta) error {
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
