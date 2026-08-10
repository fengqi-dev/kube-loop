package mirrorapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controller/storage"
	"github.com/fengqi-dev/kube-loop/internal/remotetask"
	"github.com/fengqi-dev/kube-loop/internal/servicebinding"
)

const (
	DefaultRecoveryInterval = 5 * time.Second
	DefaultStaleAfter       = 15 * time.Second
	DefaultRecoveryBatch    = 100
)

type RecoveryStorage interface {
	Tasks() storage.TaskRepository
	ResourceSnapshots() storage.ResourceSnapshotRepository
}

type RecoveryConfig struct {
	OwnerID        string
	GatewayIP      string
	Interval       time.Duration
	StaleAfter     time.Duration
	RestoreTimeout time.Duration
	BatchSize      int
	Now            func() time.Time
}

type Reconciler struct {
	storage        RecoveryStorage
	resources      ResourceMutator
	logger         *slog.Logger
	ownerID        string
	gatewayIP      string
	interval       time.Duration
	staleAfter     time.Duration
	restoreTimeout time.Duration
	batchSize      int
	now            func() time.Time
}

func NewReconciler(
	storageBackend RecoveryStorage,
	resources ResourceMutator,
	logger *slog.Logger,
	config RecoveryConfig,
) (*Reconciler, error) {
	if storageBackend == nil || resources == nil {
		return nil, errors.New("Mirror recovery storage and resource mutator are required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if config.Interval == 0 {
		config.Interval = DefaultRecoveryInterval
	}
	if config.StaleAfter == 0 {
		config.StaleAfter = DefaultStaleAfter
	}
	if config.RestoreTimeout == 0 {
		config.RestoreTimeout = defaultRestoreTimeout
	}
	if config.BatchSize == 0 {
		config.BatchSize = DefaultRecoveryBatch
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Interval < 100*time.Millisecond || config.Interval > 24*time.Hour {
		return nil, errors.New("Mirror recovery interval must be between 100ms and 24h")
	}
	if config.StaleAfter < config.Interval || config.StaleAfter > 24*time.Hour {
		return nil, errors.New("Mirror stale threshold must be at least the recovery interval and at most 24h")
	}
	if config.RestoreTimeout < time.Second || config.RestoreTimeout > 5*time.Minute {
		return nil, errors.New("Mirror recovery restore timeout must be between 1s and 5m")
	}
	if config.BatchSize < 1 || config.BatchSize > 1000 {
		return nil, errors.New("Mirror recovery batch size must be between 1 and 1000")
	}
	return &Reconciler{
		storage: storageBackend, resources: resources, logger: logger,
		ownerID: config.OwnerID, gatewayIP: config.GatewayIP,
		interval: config.Interval, staleAfter: config.StaleAfter,
		restoreTimeout: config.RestoreTimeout, batchSize: config.BatchSize, now: config.Now,
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
		return 0, errors.New("Mirror recovery context is required")
	}
	now := reconciler.now().UTC()
	tasks, err := reconciler.storage.Tasks().ListStaleByTypeStates(
		ctx, TaskType, []remotetask.State{remotetask.Starting, remotetask.Running, remotetask.Stopping, remotetask.Recovering},
		now.Add(-reconciler.staleAfter), reconciler.batchSize,
	)
	if err != nil {
		return 0, err
	}
	recovered := 0
	var result error
	for _, task := range tasks {
		claimed, recoverErr := reconciler.recover(ctx, task, now)
		if claimed {
			recovered++
		}
		if recoverErr != nil {
			result = errors.Join(result, recoverErr)
		}
	}
	return recovered, result
}

func (reconciler *Reconciler) recover(ctx context.Context, task storage.Task, now time.Time) (bool, error) {
	claim := ownerResult{OwnerID: reconciler.ownerID, GatewayIP: reconciler.gatewayIP}
	claimJSON, _ := json.Marshal(claim)
	if err := reconciler.storage.Tasks().ClaimStale(
		ctx, task.ID, task.State, task.UpdatedAt, remotetask.Recovering, claimJSON, now,
	); err != nil {
		if errors.Is(err, storage.ErrConflict) || errors.Is(err, storage.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	snapshots, err := reconciler.storage.ResourceSnapshots().ListByTask(ctx, task.ID)
	if err != nil {
		reconciler.deferRecovery(task.ID, "read rollback snapshot failed")
		return true, err
	}
	if len(snapshots) != 1 || snapshots[0].Kind != mirrorSnapshotKind {
		return true, reconciler.failWithoutSnapshot(task.ID)
	}
	var snapshot servicebinding.ServiceInterceptSnapshot
	if err := json.Unmarshal(snapshots[0].Data, &snapshot); err != nil || snapshot.Service == "" || snapshot.Namespace == "" {
		return true, reconciler.failWithoutSnapshot(task.ID)
	}
	restoreContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), reconciler.restoreTimeout)
	err = reconciler.resources.Restore(restoreContext, snapshot, task.ID)
	cancel()
	if err != nil {
		reconciler.deferRecovery(task.ID, "Mirror resource restoration is pending")
		return true, err
	}
	next := remotetask.Failed
	message := "Mirror owner was lost"
	if task.State == remotetask.Stopping {
		next, message = remotetask.Stopped, ""
	}
	terminal := ownerResult{OwnerID: reconciler.ownerID, GatewayIP: reconciler.gatewayIP, Restored: true, Error: message}
	terminalJSON, _ := json.Marshal(terminal)
	if err := reconciler.storage.Tasks().UpdateState(ctx, task.ID, remotetask.Recovering, next, terminalJSON, reconciler.now().UTC()); err != nil {
		if !errors.Is(err, storage.ErrConflict) {
			return true, err
		}
		current, getErr := reconciler.storage.Tasks().GetByID(ctx, task.ID)
		if getErr != nil {
			return true, getErr
		}
		if current.State != remotetask.Stopping {
			return true, err
		}
		if updateErr := reconciler.storage.Tasks().UpdateState(
			ctx, task.ID, remotetask.Stopping, remotetask.Stopped, terminalJSON, reconciler.now().UTC(),
		); updateErr != nil {
			return true, updateErr
		}
	}
	_, err = reconciler.storage.ResourceSnapshots().DeleteByTask(ctx, task.ID)
	return true, err
}

func (reconciler *Reconciler) deferRecovery(taskID, message string) {
	result := ownerResult{OwnerID: reconciler.ownerID, GatewayIP: reconciler.gatewayIP, Error: message}
	encoded, _ := json.Marshal(result)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = reconciler.storage.Tasks().UpdateState(ctx, taskID, remotetask.Recovering, remotetask.Recovering, encoded, reconciler.now().UTC())
}

func (reconciler *Reconciler) failWithoutSnapshot(taskID string) error {
	result := ownerResult{
		OwnerID: reconciler.ownerID, GatewayIP: reconciler.gatewayIP,
		Error: "Mirror rollback snapshot is missing or invalid",
	}
	encoded, _ := json.Marshal(result)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return reconciler.storage.Tasks().UpdateState(ctx, taskID, remotetask.Recovering, remotetask.Failed, encoded, reconciler.now().UTC())
}

func (reconciler *Reconciler) runAndLog(ctx context.Context) {
	count, err := reconciler.RunOnce(ctx)
	if err != nil {
		if ctx.Err() == nil {
			reconciler.logger.ErrorContext(ctx, "Mirror stale-owner recovery pass failed", "error", err)
		}
		return
	}
	if count > 0 {
		reconciler.logger.InfoContext(ctx, "Mirror stale owners reconciled", "tasks", count)
	}
}
