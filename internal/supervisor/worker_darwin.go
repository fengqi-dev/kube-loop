//go:build darwin

package supervisor

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/helper"
	supervisorprotocol "github.com/fengqi-dev/kube-loop/internal/protocol/supervisor"
)

type WorkerController interface {
	Status(context.Context) (supervisorprotocol.WorkerStatus, error)
	Stop(context.Context) error
	Start(context.Context) error
}

type launchdWorker struct {
	config Config
}

func (w launchdWorker) Status(ctx context.Context) (supervisorprotocol.WorkerStatus, error) {
	status := supervisorprotocol.WorkerStatus{}
	if _, err := exec.LookPath("launchctl"); err != nil {
		return status, err
	}
	if digest, err := fileSHA256(w.config.WorkerBinaryPath); err == nil {
		status.Installed = true
		status.SHA256 = digest
	}
	auth, err := helper.ReadSystemAuth()
	if err != nil {
		return status, fmt.Errorf("read worker auth: %w", err)
	}
	client := &helper.Client{Token: auth.Token}
	response, err := client.Status(ctx)
	if err != nil {
		return status, fmt.Errorf("query worker: %w", err)
	}
	status.Running = response.Running
	status.CoreReady = response.CoreReady
	status.Version = response.Version
	status.Protocol = response.Protocol
	status.ActiveSessions = response.ActiveSessions
	return status, nil
}

func (w launchdWorker) Stop(ctx context.Context) error {
	target := "system/" + w.config.WorkerLabel
	command := exec.CommandContext(ctx, "/bin/launchctl", "bootout", target)
	if output, err := command.CombinedOutput(); err != nil && !strings.Contains(string(output), "No such process") {
		return fmt.Errorf("bootout worker: %w: %s", err, strings.TrimSpace(string(output)))
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := exec.CommandContext(ctx, "/bin/launchctl", "print", target).Run(); err != nil {
			//nolint:nilerr // launchctl failure here confirms the worker is no longer registered.
			return nil
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return fmt.Errorf("launchd worker %s did not stop", w.config.WorkerLabel)
}

func (w launchdWorker) Start(ctx context.Context) error {
	//nolint:gosec // WorkerPlistPath is derived from the fixed supervisor configuration.
	bootstrap := exec.CommandContext(
		ctx,
		"/bin/launchctl",
		"bootstrap",
		"system",
		w.config.WorkerPlistPath,
	)
	if output, err := bootstrap.CombinedOutput(); err != nil &&
		!strings.Contains(string(output), "service already loaded") {
		return fmt.Errorf("bootstrap worker: %w: %s", err, strings.TrimSpace(string(output)))
	}
	target := "system/" + w.config.WorkerLabel
	if output, err := exec.CommandContext(ctx, "/bin/launchctl", "kickstart", "-k", target).
		CombinedOutput(); err != nil {
		return fmt.Errorf("kickstart worker: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
