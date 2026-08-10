package trafficbindingclient

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	controllerstorage "github.com/fengqi-dev/kube-loop/internal/controller/storage"
	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/internal/operator/api/v1alpha1"
)

const (
	defaultReconcileInterval = time.Minute
	defaultCleanupTimeout    = 30 * time.Second
	defaultReconcileBatch    = 100
)

type TaskReader interface {
	GetByID(context.Context, string) (controllerstorage.Task, error)
}

type ReconcilerConfig struct {
	Interval       time.Duration
	CleanupTimeout time.Duration
	BatchSize      int
}

// Reconciler removes CRs whose durable Task is gone, terminal, or no longer
// matches the immutable binding owner. This closes the crash window between a
// database cascade and the corresponding CR deletion request.
type Reconciler struct {
	manager        *Manager
	tasks          TaskReader
	logger         *slog.Logger
	interval       time.Duration
	cleanupTimeout time.Duration
	batchSize      int
}

func NewReconciler(
	manager *Manager,
	tasks TaskReader,
	logger *slog.Logger,
	config ReconcilerConfig,
) (*Reconciler, error) {
	if manager == nil || tasks == nil {
		return nil, errors.New("TrafficBinding manager and Task reader are required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if config.Interval == 0 {
		config.Interval = defaultReconcileInterval
	}
	if config.CleanupTimeout == 0 {
		config.CleanupTimeout = defaultCleanupTimeout
	}
	if config.BatchSize == 0 {
		config.BatchSize = defaultReconcileBatch
	}
	if config.Interval < 100*time.Millisecond || config.Interval > 24*time.Hour {
		return nil, errors.New("TrafficBinding reconciliation interval must be between 100ms and 24h")
	}
	if config.CleanupTimeout < time.Second || config.CleanupTimeout > 5*time.Minute {
		return nil, errors.New("TrafficBinding cleanup timeout must be between 1s and 5m")
	}
	if config.BatchSize < 1 || config.BatchSize > 1000 {
		return nil, errors.New("TrafficBinding reconciliation batch size must be between 1 and 1000")
	}
	return &Reconciler{
		manager: manager, tasks: tasks, logger: logger,
		interval: config.Interval, cleanupTimeout: config.CleanupTimeout, batchSize: config.BatchSize,
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
	bindings := &trafficv1alpha1.TrafficBindingList{}
	if err := reconciler.manager.client.List(ctx, bindings, client.MatchingLabels{managedByLabel: managedByValue}, client.Limit(reconciler.batchSize)); err != nil {
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

func (reconciler *Reconciler) orphaned(
	ctx context.Context,
	binding *trafficv1alpha1.TrafficBinding,
) (bool, error) {
	task, err := reconciler.tasks.GetByID(ctx, binding.Spec.TaskID)
	if errors.Is(err, controllerstorage.ErrNotFound) {
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
	removed, err := reconciler.RunOnce(ctx)
	if err != nil {
		if ctx.Err() == nil {
			reconciler.logger.ErrorContext(ctx, "TrafficBinding orphan reconciliation failed", "error", err)
		}
		return
	}
	if removed > 0 {
		reconciler.logger.InfoContext(ctx, "Removed orphaned TrafficBindings", "bindings", removed)
	}
}
