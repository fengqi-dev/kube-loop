package runtime

import (
	"context"
	"fmt"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/helper"
	helperinstall "github.com/fengqi-dev/kube-loop/internal/helper/install"
	"github.com/fengqi-dev/kube-loop/internal/protocol/sessionspec"
)

// NewPrivileged returns a Runtime backed by the narrowly scoped local helper.
// The helper is installed lazily, so SOCKS-only clients remain unprivileged.
func NewPrivileged() *Runtime {
	runtime := &Runtime{}
	runtime.PrivilegedStart = func(
		ctx context.Context,
		spec sessionspec.Spec,
	) (func(context.Context) error, error) {
		if err := helperinstall.EnsureInstall(ctx); err != nil {
			return nil, fmt.Errorf("ensure privileged helper: %w", err)
		}
		client, err := helper.NewClient()
		if err != nil {
			return nil, err
		}
		if _, err := client.Start(ctx, spec); err != nil {
			if !strings.Contains(err.Error(), "another privileged TUN session is already active") {
				return nil, fmt.Errorf("helper start session: %w", err)
			}
			if _, stopErr := client.StopAll(ctx); stopErr != nil {
				return nil, fmt.Errorf("helper start session: %w (stop-all: %w)", err, stopErr)
			}
			if _, err := client.Start(ctx, spec); err != nil {
				return nil, fmt.Errorf("helper start session: %w", err)
			}
		}
		return func(stopCtx context.Context) error {
			_, err := client.Stop(stopCtx, spec.ID)
			return err
		}, nil
	}
	runtime.PrivilegedUpdateDNS = func(
		ctx context.Context,
		sessionID string,
		dns sessionspec.DNSMeta,
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
	runtime.PrivilegedReadLogs = func(
		ctx context.Context,
		sessionID string,
		offset int64,
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
