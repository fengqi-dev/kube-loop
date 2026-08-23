//go:build darwin

package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	supervisorprotocol "github.com/fengqi-dev/kube-loop/internal/protocol/supervisor"
)

func (u *Updater) Recover(ctx context.Context) error {
	if !fileExists(u.config.JournalPath()) {
		return nil
	}
	status, err := u.worker.Status(ctx)
	if err == nil && status.Running && status.CoreReady {
		return os.Remove(u.config.JournalPath())
	}
	if !fileExists(u.config.PreviousPath()) {
		return fmt.Errorf("update journal exists but no previous worker is available")
	}
	rolledBack, rollbackErr := u.rollback(ctx, fmt.Errorf("recover interrupted update"))
	if !rolledBack {
		return rollbackErr
	}
	return os.Remove(u.config.JournalPath())
}

func (u *Updater) activate(
	ctx context.Context,
	staged string,
	manifest supervisorprotocol.UpdateManifest,
) (bool, error) {
	if err := u.worker.Stop(ctx); err != nil {
		return false, err
	}
	previous := u.config.PreviousPath()
	_ = os.Remove(previous)
	if fileExists(u.config.WorkerBinaryPath) {
		if err := os.Rename(u.config.WorkerBinaryPath, previous); err != nil {
			_ = u.worker.Start(ctx)
			return false, fmt.Errorf("preserve previous worker: %w", err)
		}
	}
	if err := u.writeJournal(journal{
		RequestID: manifest.RequestID,
		Phase:     "swapping",
		Version:   manifest.Version,
		SHA256:    manifest.SHA256,
	}); err != nil {
		return u.rollback(ctx, err)
	}
	if err := os.Rename(staged, u.config.WorkerBinaryPath); err != nil {
		return u.rollback(ctx, fmt.Errorf("activate staged worker: %w", err))
	}
	//nolint:gosec // The installed worker must be executable by launchd.
	if err := os.Chmod(u.config.WorkerBinaryPath, 0o755); err != nil {
		return u.rollback(ctx, fmt.Errorf("secure worker executable: %w", err))
	}
	if err := syncDir(filepath.Dir(u.config.WorkerBinaryPath)); err != nil {
		return u.rollback(ctx, fmt.Errorf("sync worker directory: %w", err))
	}
	if err := u.worker.Start(ctx); err != nil {
		return u.rollback(ctx, err)
	}
	if err := u.waitReady(ctx, manifest); err != nil {
		return u.rollback(ctx, err)
	}
	return false, nil
}

func (u *Updater) waitReady(ctx context.Context, manifest supervisorprotocol.UpdateManifest) error {
	waitCtx, cancel := context.WithTimeout(ctx, u.readyTimeout)
	defer cancel()
	var lastErr error
	for {
		status, err := u.worker.Status(waitCtx)
		if err == nil && status.Running && status.CoreReady && status.Version == manifest.Version &&
			status.Protocol == manifest.WorkerProtocol && status.SHA256 == manifest.SHA256 {
			return nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf(
				"worker readiness mismatch: version=%q protocol=%d coreReady=%v sha256=%q",
				status.Version,
				status.Protocol,
				status.CoreReady,
				status.SHA256,
			)
		}
		timer := time.NewTimer(u.readyInterval)
		select {
		case <-waitCtx.Done():
			timer.Stop()
			return fmt.Errorf("worker did not become ready: %w", lastErr)
		case <-timer.C:
		}
	}
}

func (u *Updater) rollback(ctx context.Context, cause error) (bool, error) {
	if err := u.worker.Stop(ctx); err != nil {
		return false, errors.Join(
			cause,
			fmt.Errorf("stop current worker before rollback: %w", err),
		)
	}
	if !fileExists(u.config.PreviousPath()) {
		return false, cause
	}
	_ = os.Remove(u.config.WorkerBinaryPath)
	if err := os.Rename(u.config.PreviousPath(), u.config.WorkerBinaryPath); err != nil {
		return false, errors.Join(cause, fmt.Errorf("restore previous worker: %w", err))
	}
	if err := u.worker.Start(ctx); err != nil {
		return false, errors.Join(cause, fmt.Errorf("restart previous worker: %w", err))
	}
	if err := u.waitAnyReady(ctx); err != nil {
		return false, errors.Join(cause, fmt.Errorf("previous worker did not recover: %w", err))
	}
	return true, cause
}

func (u *Updater) waitAnyReady(ctx context.Context) error {
	waitCtx, cancel := context.WithTimeout(ctx, u.readyTimeout)
	defer cancel()
	var lastErr error
	for {
		status, err := u.worker.Status(waitCtx)
		if err == nil && status.Running && status.CoreReady {
			return nil
		}
		lastErr = err
		timer := time.NewTimer(u.readyInterval)
		select {
		case <-waitCtx.Done():
			timer.Stop()
			if lastErr != nil {
				return lastErr
			}
			return waitCtx.Err()
		case <-timer.C:
		}
	}
}

func (u *Updater) Rollback(ctx context.Context) supervisorprotocol.Response {
	u.mu.Lock()
	defer u.mu.Unlock()
	response := supervisorprotocol.Response{Protocol: supervisorprotocol.Version, Channel: u.config.Channel}
	if !fileExists(u.config.PreviousPath()) {
		response.Error = "no previous worker is available"
		return response
	}
	rolledBack, err := u.rollback(ctx, fmt.Errorf("manual rollback"))
	response.RolledBack = rolledBack
	response.Worker, _ = u.Status(ctx)
	if err != nil && !rolledBack {
		response.Error = err.Error()
		return response
	}
	response.OK = true
	return response
}

func (u *Updater) Restart(ctx context.Context) supervisorprotocol.Response {
	u.mu.Lock()
	defer u.mu.Unlock()
	response := supervisorprotocol.Response{Protocol: supervisorprotocol.Version, Channel: u.config.Channel}
	if err := u.worker.Stop(ctx); err != nil {
		response.Error = err.Error()
		return response
	}
	if err := u.worker.Start(ctx); err != nil {
		response.Error = err.Error()
		return response
	}
	if err := u.waitAnyReady(ctx); err != nil {
		response.Worker, _ = u.Status(ctx)
		response.Error = fmt.Sprintf(
			"worker did not become ready after restart: %v",
			err,
		)
		return response
	}
	var statusErr error
	response.Worker, statusErr = u.Status(ctx)
	if statusErr != nil {
		response.Error = statusErr.Error()
		return response
	}
	response.OK = response.Worker.Running && response.Worker.CoreReady
	if !response.OK {
		response.Error = "worker did not become ready after restart"
	}
	return response
}
