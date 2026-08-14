package trafficbindingclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
	controlplanestorage "github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

const (
	defaultReconcileInterval = 5 * time.Second
	defaultStaleAfter        = 15 * time.Second
	defaultCleanupTimeout    = 30 * time.Second
	defaultReconcileBatch    = 100
)

var recoverableTaskTypes = []string{"exchange", "mirror", "preview"}
var recoverableStates = []remotetask.State{
	remotetask.Starting, remotetask.Running, remotetask.Stopping, remotetask.Recovering,
}

type TaskStore interface {
	GetByID(context.Context, string) (controlplanestorage.Task, error)
	ListStaleByTypeStates(context.Context, string, []remotetask.State, time.Time, int) ([]controlplanestorage.Task, error)
	ClaimStale(context.Context, string, remotetask.State, time.Time, remotetask.State, json.RawMessage, time.Time) error
	UpdateState(context.Context, string, remotetask.State, remotetask.State, json.RawMessage, time.Time) error
}

type SessionReader interface {
	GetByID(context.Context, string) (controlplanestorage.Session, error)
}

type ReconcilerConfig struct {
	Interval       time.Duration
	StaleAfter     time.Duration
	CleanupTimeout time.Duration
	BatchSize      int
	Now            func() time.Time
}

// Reconciler owns Control Plane-side Task-to-CR consistency. Kubernetes resource
// restoration remains exclusively owned by the Operator finalizer.
type Reconciler struct {
	manager        *Manager
	tasks          TaskStore
	sessions       SessionReader
	logger         *slog.Logger
	interval       time.Duration
	staleAfter     time.Duration
	cleanupTimeout time.Duration
	batchSize      int
	now            func() time.Time
}

func NewReconciler(
	manager *Manager,
	tasks TaskStore,
	sessions SessionReader,
	logger *slog.Logger,
	config ReconcilerConfig,
) (*Reconciler, error) {
	if manager == nil || tasks == nil || sessions == nil {
		return nil, errors.New("TrafficBinding manager, Task store and Session reader are required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if config.Interval == 0 {
		config.Interval = defaultReconcileInterval
	}
	if config.StaleAfter == 0 {
		config.StaleAfter = defaultStaleAfter
	}
	if config.CleanupTimeout == 0 {
		config.CleanupTimeout = defaultCleanupTimeout
	}
	if config.BatchSize == 0 {
		config.BatchSize = defaultReconcileBatch
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Interval < 100*time.Millisecond || config.Interval > 24*time.Hour {
		return nil, errors.New("TrafficBinding reconciliation interval must be between 100ms and 24h")
	}
	if config.StaleAfter < config.Interval || config.StaleAfter > 24*time.Hour {
		return nil, errors.New("TrafficBinding stale threshold must be at least the reconciliation interval and at most 24h")
	}
	if config.CleanupTimeout < time.Second || config.CleanupTimeout > 5*time.Minute {
		return nil, errors.New("TrafficBinding cleanup timeout must be between 1s and 5m")
	}
	if config.BatchSize < 1 || config.BatchSize > 1000 {
		return nil, errors.New("TrafficBinding reconciliation batch size must be between 1 and 1000")
	}
	return &Reconciler{
		manager: manager, tasks: tasks, sessions: sessions, logger: logger,
		interval: config.Interval, staleAfter: config.StaleAfter,
		cleanupTimeout: config.CleanupTimeout, batchSize: config.BatchSize, now: config.Now,
	}, nil
}

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
		return 0, errors.New("TrafficBinding reconciliation context is required")
	}
	recovered, recoveryErr := reconciler.recoverStaleTasks(ctx)
	removed, orphanErr := reconciler.removeOrphanedBindings(ctx)
	return recovered + removed, errors.Join(recoveryErr, orphanErr)
}

func (reconciler *Reconciler) recoverStaleTasks(ctx context.Context) (int, error) {
	now := reconciler.now().UTC()
	before := now.Add(-reconciler.staleAfter)
	recovered := 0
	var result error
	for _, taskType := range recoverableTaskTypes {
		tasks, err := reconciler.tasks.ListStaleByTypeStates(ctx, taskType, recoverableStates, before, reconciler.batchSize)
		if err != nil {
			result = errors.Join(result, fmt.Errorf("list stale %s Tasks: %w", taskType, err))
			continue
		}
		for _, task := range tasks {
			claimed, err := reconciler.recoverTask(ctx, task, now)
			if claimed {
				recovered++
			}
			result = errors.Join(result, err)
		}
	}
	return recovered, result
}

func (reconciler *Reconciler) recoverTask(ctx context.Context, task controlplanestorage.Task, now time.Time) (bool, error) {
	if err := reconciler.tasks.ClaimStale(
		ctx, task.ID, task.State, task.UpdatedAt, remotetask.Recovering, task.Result, now,
	); err != nil {
		if errors.Is(err, controlplanestorage.ErrConflict) || errors.Is(err, controlplanestorage.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	session, err := reconciler.sessions.GetByID(ctx, task.SessionID)
	if err != nil {
		reconciler.deferRecovery(task, now)
		return true, fmt.Errorf("read Session %s for stale Task %s: %w", task.SessionID, task.ID, err)
	}
	cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), reconciler.cleanupTimeout)
	err = reconciler.manager.Delete(cleanupContext, session.Namespace, task.ID)
	cancel()
	if err != nil {
		reconciler.deferRecovery(task, reconciler.now().UTC())
		return true, fmt.Errorf("delete TrafficBinding for stale Task %s: %w", task.ID, err)
	}
	next := remotetask.Failed
	if task.State == remotetask.Stopping {
		next = remotetask.Stopped
	}
	if err := reconciler.tasks.UpdateState(ctx, task.ID, remotetask.Recovering, next, task.Result, reconciler.now().UTC()); err != nil {
		if !errors.Is(err, controlplanestorage.ErrConflict) {
			return true, err
		}
		current, getErr := reconciler.tasks.GetByID(ctx, task.ID)
		if getErr != nil {
			return true, getErr
		}
		if current.State != remotetask.Stopping {
			return true, err
		}
		return true, reconciler.tasks.UpdateState(ctx, task.ID, remotetask.Stopping, remotetask.Stopped, current.Result, reconciler.now().UTC())
	}
	return true, nil
}

func (reconciler *Reconciler) deferRecovery(task controlplanestorage.Task, now time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = reconciler.tasks.UpdateState(ctx, task.ID, remotetask.Recovering, remotetask.Recovering, task.Result, now)
}

func (reconciler *Reconciler) removeOrphanedBindings(ctx context.Context) (int, error) {
	bindings := &trafficv1alpha1.TrafficBindingList{}
	if err := reconciler.manager.client.List(ctx, bindings, client.MatchingLabels{
		managedByLabel: managedByValue, controlPlaneIDLabel: reconciler.manager.controlPlaneID,
	}, client.Limit(reconciler.batchSize)); err != nil {
		return 0, fmt.Errorf("list TrafficBindings: %w", err)
	}
	removed := 0
	var result error
	for index := range bindings.Items {
		binding := &bindings.Items[index]
		orphaned, err := reconciler.orphaned(ctx, binding)
		if err != nil {
			result = errors.Join(result, err)
			continue
		}
		if !orphaned {
			continue
		}
		cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), reconciler.cleanupTimeout)
		err = reconciler.manager.Delete(cleanupContext, binding.Namespace, binding.Spec.TaskID)
		cancel()
		if err != nil {
			result = errors.Join(result, err)
			continue
		}
		removed++
	}
	return removed, result
}

func (reconciler *Reconciler) orphaned(ctx context.Context, binding *trafficv1alpha1.TrafficBinding) (bool, error) {
	task, err := reconciler.tasks.GetByID(ctx, binding.Spec.TaskID)
	if errors.Is(err, controlplanestorage.ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("read Task %s for TrafficBinding %s/%s: %w", binding.Spec.TaskID, binding.Namespace, binding.Name, err)
	}
	expectedType, ok := taskTypeForMode(binding.Spec.Mode)
	return task.SessionID != binding.Spec.SessionID || task.Type != expectedType || !ok || task.State.Terminal(), nil
}

func taskTypeForMode(mode trafficv1alpha1.TrafficBindingMode) (string, bool) {
	switch mode {
	case trafficv1alpha1.TrafficBindingModePortForward:
		return "port-forward", true
	case trafficv1alpha1.TrafficBindingModePreview:
		return "preview", true
	case trafficv1alpha1.TrafficBindingModeExchange:
		return "exchange", true
	case trafficv1alpha1.TrafficBindingModeMirror:
		return "mirror", true
	default:
		return "", false
	}
}

func (reconciler *Reconciler) runAndLog(ctx context.Context) {
	count, err := reconciler.RunOnce(ctx)
	if err != nil {
		if ctx.Err() == nil {
			reconciler.logger.ErrorContext(ctx, "TrafficBinding reconciliation failed", "error", err)
		}
		return
	}
	if count > 0 {
		reconciler.logger.InfoContext(ctx, "Reconciled TrafficBinding ownership", "resources", count)
	}
}
