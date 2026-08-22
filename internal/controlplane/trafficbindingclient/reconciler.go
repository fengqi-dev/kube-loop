package trafficbindingclient

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	controlplanestorage "github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

const (
	defaultReconcileInterval = 5 * time.Second
	defaultStaleAfter        = 15 * time.Second
	defaultCleanupTimeout    = 30 * time.Second
	defaultReconcileBatch    = 100
)

var recoverableTaskTypes = []string{taskTypeExchange, "mirror", "preview"}
var recoverableStates = []remotetask.State{
	remotetask.Starting, remotetask.Running, remotetask.Stopping, remotetask.Recovering,
}

type TaskStore interface {
	GetByID(context.Context, string) (controlplanestorage.Task, error)
	ListStaleByTypeStates(
		context.Context,
		string,
		[]remotetask.State,
		time.Time,
		int,
	) ([]controlplanestorage.Task, error)
	ClaimStale(
		context.Context,
		string,
		remotetask.State,
		time.Time,
		remotetask.State,
		json.RawMessage,
		time.Time,
	) error
	UpdateState(
		context.Context,
		string,
		remotetask.State,
		remotetask.State,
		json.RawMessage,
		time.Time,
	) error
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
		return nil, errors.New(
			"traffic binding manager, Task store and Session reader are required",
		)
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
	if config.Interval < 100*time.Millisecond ||
		config.Interval > 24*time.Hour {
		return nil, errors.New(
			"traffic binding reconciliation interval must be between 100ms and 24h",
		)
	}
	if config.StaleAfter < config.Interval || config.StaleAfter > 24*time.Hour {
		return nil, errors.New(
			"traffic binding stale threshold must be at least the reconciliation interval and at most 24h",
		)
	}
	if config.CleanupTimeout < time.Second ||
		config.CleanupTimeout > 5*time.Minute {
		return nil, errors.New(
			"traffic binding cleanup timeout must be between 1s and 5m",
		)
	}
	if config.BatchSize < 1 || config.BatchSize > 1000 {
		return nil, errors.New(
			"traffic binding reconciliation batch size must be between 1 and 1000",
		)
	}
	return &Reconciler{
		manager: manager, tasks: tasks, sessions: sessions, logger: logger,
		interval: config.Interval, staleAfter: config.StaleAfter,
		cleanupTimeout: config.CleanupTimeout, batchSize: config.BatchSize, now: config.Now,
	}, nil
}
