package previewapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controller/storage"
	"github.com/fengqi-dev/kube-loop/internal/remotetask"
	"github.com/fengqi-dev/kube-loop/internal/servicebinding"
	"github.com/google/uuid"
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
	OwnerID       string
	GatewayIP     string
	Interval      time.Duration
	StaleAfter    time.Duration
	DeleteTimeout time.Duration
	BatchSize     int
	Now           func() time.Time
}

type Reconciler struct {
	storage       RecoveryStorage
	resources     ResourceManager
	logger        *slog.Logger
	ownerID       string
	gatewayIP     string
	interval      time.Duration
	staleAfter    time.Duration
	deleteTimeout time.Duration
	batchSize     int
	now           func() time.Time
}

func NewReconciler(
	storageBackend RecoveryStorage,
	resources ResourceManager,
	logger *slog.Logger,
	config RecoveryConfig,
) (*Reconciler, error) {
	if storageBackend == nil || resources == nil {
		return nil, errors.New("Preview recovery storage and resource manager are required")
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
	if config.DeleteTimeout == 0 {
		config.DeleteTimeout = defaultDeleteTimeout
	}
	if config.BatchSize == 0 {
		config.BatchSize = DefaultRecoveryBatch
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	config.GatewayIP = strings.TrimSpace(config.GatewayIP)
	address := net.ParseIP(config.GatewayIP)
	if address == nil || address.IsUnspecified() {
		return nil, errors.New("Preview recovery Gateway IP must be a concrete IP address")
	}
	config.OwnerID = strings.TrimSpace(config.OwnerID)
	if config.OwnerID == "" {
		config.OwnerID = uuid.NewString()
	}
	if len(config.OwnerID) > 253 {
		return nil, errors.New("Preview recovery owner ID is too long")
	}
	if config.Interval < 100*time.Millisecond || config.Interval > 24*time.Hour {
		return nil, errors.New("Preview recovery interval must be between 100ms and 24h")
	}
	if config.StaleAfter < config.Interval || config.StaleAfter > 24*time.Hour {
		return nil, errors.New("Preview stale threshold must be at least the recovery interval and at most 24h")
	}
	if config.DeleteTimeout < time.Second || config.DeleteTimeout > 5*time.Minute {
		return nil, errors.New("Preview recovery delete timeout must be between 1s and 5m")
	}
	if config.BatchSize < 1 || config.BatchSize > 1000 {
		return nil, errors.New("Preview recovery batch size must be between 1 and 1000")
	}
	return &Reconciler{
		storage: storageBackend, resources: resources, logger: logger,
		ownerID: config.OwnerID, gatewayIP: config.GatewayIP,
		interval: config.Interval, staleAfter: config.StaleAfter,
		deleteTimeout: config.DeleteTimeout, batchSize: config.BatchSize, now: config.Now,
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
		return 0, errors.New("Preview recovery context is required")
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
	var previous ownerResult
	if json.Unmarshal(task.Result, &previous) == nil {
		claim.ClusterIP = previous.ClusterIP
	}
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
		reconciler.deferRecovery(task.ID, claim.ClusterIP, "read cleanup intent failed")
		return true, err
	}
	if len(snapshots) != 1 || snapshots[0].Kind != previewSnapshotKind {
		return true, reconciler.failWithoutSnapshot(task.ID, claim.ClusterIP)
	}
	var snapshot servicebinding.PreviewServiceSnapshot
	if err := json.Unmarshal(snapshots[0].Data, &snapshot); err != nil || snapshot.Service == "" || snapshot.Namespace == "" {
		return true, reconciler.failWithoutSnapshot(task.ID, claim.ClusterIP)
	}
	deleteContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), reconciler.deleteTimeout)
	err = reconciler.resources.Delete(deleteContext, snapshot, task.ID)
	cancel()
	if err != nil {
		reconciler.deferRecovery(task.ID, claim.ClusterIP, "Preview resource deletion is pending")
		return true, err
	}
	next := remotetask.Failed
	message := "Preview owner was lost"
	if task.State == remotetask.Stopping {
		next, message = remotetask.Stopped, ""
	}
	terminal := ownerResult{
		OwnerID: reconciler.ownerID, GatewayIP: reconciler.gatewayIP,
		ClusterIP: claim.ClusterIP, Deleted: true, Error: message,
	}
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

func (reconciler *Reconciler) deferRecovery(taskID, clusterIP, message string) {
	result := ownerResult{
		OwnerID: reconciler.ownerID, GatewayIP: reconciler.gatewayIP,
		ClusterIP: clusterIP, Error: message,
	}
	encoded, _ := json.Marshal(result)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = reconciler.storage.Tasks().UpdateState(ctx, taskID, remotetask.Recovering, remotetask.Recovering, encoded, reconciler.now().UTC())
}

func (reconciler *Reconciler) failWithoutSnapshot(taskID, clusterIP string) error {
	result := ownerResult{
		OwnerID: reconciler.ownerID, GatewayIP: reconciler.gatewayIP, ClusterIP: clusterIP,
		Error: "Preview cleanup intent is missing or invalid",
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
			reconciler.logger.ErrorContext(ctx, "Preview stale-owner recovery pass failed", "error", err)
		}
		return
	}
	if count > 0 {
		reconciler.logger.InfoContext(ctx, "Preview stale owners reconciled", "tasks", count)
	}
}
