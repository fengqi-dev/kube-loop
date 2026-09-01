package sessionregistry

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/periodic"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

const (
	DefaultRecoveryInterval = 5 * time.Second
	DefaultStaleAfter       = 15 * time.Second
	DefaultRecoveryBatch    = 100
)

type RecoveryStore interface {
	Tasks() storage.TaskRepository
}

type RecoveryConfig struct {
	Interval   time.Duration
	StaleAfter time.Duration
	BatchSize  int
	TaskTypes  []string
	Now        func() time.Time
}

type Reconciler struct {
	store      RecoveryStore
	logger     *slog.Logger
	interval   time.Duration
	staleAfter time.Duration
	batchSize  int
	taskTypes  []string
	now        func() time.Time
}

func NewReconciler(
	store RecoveryStore,
	logger *slog.Logger,
	config RecoveryConfig,
) (*Reconciler, error) {
	if store == nil {
		return nil, errors.New("session runtime recovery store is required")
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
	if config.BatchSize == 0 {
		config.BatchSize = DefaultRecoveryBatch
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if len(config.TaskTypes) == 0 {
		config.TaskTypes = []string{taskTypePodExec, "file-transfer"}
	}
	if config.Interval < 100*time.Millisecond || config.Interval > 24*time.Hour ||
		config.StaleAfter < config.Interval ||
		config.StaleAfter > 24*time.Hour {
		return nil, errors.New("session runtime recovery intervals are invalid")
	}
	if config.BatchSize < 1 || config.BatchSize > 1000 ||
		len(config.TaskTypes) > 16 {
		return nil, errors.New(
			"session runtime recovery batch or Task types are invalid",
		)
	}
	seen := make(map[string]struct{}, len(config.TaskTypes))
	taskTypes := make([]string, 0, len(config.TaskTypes))
	for _, taskType := range config.TaskTypes {
		taskType = strings.TrimSpace(taskType)
		if taskType == "" || len(taskType) > 128 {
			return nil, errors.New(
				"session runtime recovery Task type is invalid",
			)
		}
		if _, exists := seen[taskType]; exists {
			continue
		}
		seen[taskType] = struct{}{}
		taskTypes = append(taskTypes, taskType)
	}
	return &Reconciler{
		store: store, logger: logger, interval: config.Interval, staleAfter: config.StaleAfter,
		batchSize: config.BatchSize, taskTypes: taskTypes, now: config.Now,
	}, nil
}

func (reconciler *Reconciler) Run(ctx context.Context) {
	periodic.Run(ctx, reconciler.interval, reconciler.runAndLog)
}

func (reconciler *Reconciler) RunOnce(ctx context.Context) (int, error) {
	if ctx == nil {
		return 0, errors.New("session runtime recovery context is required")
	}
	now := reconciler.now().UTC()
	recovered := 0
	var result error
	for _, taskType := range reconciler.taskTypes {
		tasks, err := reconciler.store.Tasks().ListStaleByTypeStates(
			ctx, taskType, []remotetask.State{remotetask.Starting, remotetask.Running, remotetask.Stopping},
			now.Add(-reconciler.staleAfter), reconciler.batchSize,
		)
		if err != nil {
			result = errors.Join(result, err)
			continue
		}
		for _, task := range tasks {
			next := remotetask.Failed
			message := "Control Plane stream owner was lost"
			if task.State == remotetask.Stopping {
				next, message = remotetask.Stopped, ""
			}
			encoded := recoveryResult(task.Result, message)
			err := reconciler.store.Tasks().ClaimStale(
				ctx, task.ID, task.State, task.UpdatedAt, next, encoded, now,
			)
			if errors.Is(err, storage.ErrConflict) ||
				errors.Is(err, storage.ErrNotFound) {
				continue
			}
			if err != nil {
				result = errors.Join(result, err)
				continue
			}
			recovered++
		}
	}
	return recovered, result
}

func recoveryResult(previous json.RawMessage, message string) json.RawMessage {
	result := make(map[string]any)
	_ = json.Unmarshal(previous, &result)
	if message == "" {
		delete(result, "error")
	} else {
		result["error"] = message
	}
	encoded, _ := json.Marshal(result)
	return encoded
}

func (reconciler *Reconciler) runAndLog(ctx context.Context) {
	count, err := reconciler.RunOnce(ctx)
	if err != nil {
		if ctx.Err() == nil {
			reconciler.logger.ErrorContext(
				ctx,
				"Session runtime recovery pass failed",
				"error",
				err,
			)
		}
		return
	}
	if count > 0 {
		reconciler.logger.InfoContext(
			ctx,
			"Recovered orphaned Session stream Tasks",
			"tasks",
			count,
		)
	}
}
