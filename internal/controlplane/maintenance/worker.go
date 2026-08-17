package maintenance

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

const (
	DefaultInterval  = time.Minute
	DefaultTimeout   = 10 * time.Second
	DefaultBatchSize = 100
)

type Config struct {
	Interval  time.Duration
	Timeout   time.Duration
	BatchSize int
	Now       func() time.Time
}

type Report struct {
	Sessions             int64
	AdminSessions        int64
	OAuthSessions        int64
	OAuthAuthorizations  int64
	OAuthBrowserSessions int64
	Idempotency          int64
}

func (report Report) Total() int64 {
	return report.Sessions + report.AdminSessions + report.OAuthSessions + report.OAuthAuthorizations + report.OAuthBrowserSessions + report.Idempotency
}

type Worker struct {
	repositories storage.Repositories
	logger       *slog.Logger
	interval     time.Duration
	timeout      time.Duration
	batchSize    int
	now          func() time.Time
}

func New(repositories storage.Repositories, logger *slog.Logger, config Config) (*Worker, error) {
	if repositories == nil {
		return nil, errors.New("Control Plane maintenance storage is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if config.Interval == 0 {
		config.Interval = DefaultInterval
	}
	if config.Timeout == 0 {
		config.Timeout = DefaultTimeout
	}
	if config.BatchSize == 0 {
		config.BatchSize = DefaultBatchSize
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Interval < 10*time.Millisecond || config.Interval > 24*time.Hour {
		return nil, errors.New("Control Plane maintenance interval must be between 10ms and 24h")
	}
	if config.Timeout <= 0 || config.Timeout > time.Minute {
		return nil, errors.New("Control Plane maintenance timeout must be between 1ns and 1m")
	}
	if config.BatchSize <= 0 || config.BatchSize > 1000 {
		return nil, errors.New("Control Plane maintenance batch size must be between 1 and 1000")
	}
	return &Worker{
		repositories: repositories,
		logger:       logger,
		interval:     config.Interval,
		timeout:      config.Timeout,
		batchSize:    config.BatchSize,
		now:          config.Now,
	}, nil
}

func (worker *Worker) Run(ctx context.Context) {
	worker.runAndLog(ctx)
	ticker := time.NewTicker(worker.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			worker.runAndLog(ctx)
		}
	}
}

func (worker *Worker) RunOnce(ctx context.Context) (Report, error) {
	if ctx == nil {
		return Report{}, errors.New("Control Plane maintenance context is required")
	}
	operationContext, cancel := context.WithTimeout(ctx, worker.timeout)
	defer cancel()
	before := worker.now().UTC()
	report := Report{}
	var result error

	count, err := worker.repositories.OAuthAuthorizationRequests().DeleteExpired(operationContext, before, worker.batchSize)
	report.OAuthAuthorizations = count
	if err != nil {
		result = errors.Join(result, fmt.Errorf("clean expired OAuth authorization requests: %w", err))
	}
	count, err = worker.repositories.AdminSessions().DeleteExpired(operationContext, before, worker.batchSize)
	report.AdminSessions = count
	if err != nil {
		result = errors.Join(result, fmt.Errorf("clean expired Management Sessions: %w", err))
	}
	count, err = worker.repositories.Idempotency().DeleteExpired(operationContext, before, worker.batchSize)
	report.Idempotency = count
	if err != nil {
		result = errors.Join(result, fmt.Errorf("clean expired idempotency records: %w", err))
	}
	count, err = worker.repositories.Sessions().DeleteExpired(operationContext, before, worker.batchSize)
	report.Sessions = count
	if err != nil {
		result = errors.Join(result, fmt.Errorf("clean expired Sessions and Tasks: %w", err))
	}
	count, err = worker.repositories.OAuthSessions().DeleteExpired(operationContext, before, worker.batchSize)
	report.OAuthSessions = count
	if err != nil {
		result = errors.Join(result, fmt.Errorf("clean expired OAuth sessions: %w", err))
	}
	count, err = worker.repositories.OAuthBrowserSessions().DeleteExpired(operationContext, before, worker.batchSize)
	report.OAuthBrowserSessions = count
	if err != nil {
		result = errors.Join(result, fmt.Errorf("clean expired OAuth browser sessions: %w", err))
	}
	return report, result
}

func (worker *Worker) runAndLog(ctx context.Context) {
	report, err := worker.RunOnce(ctx)
	if err != nil {
		if ctx.Err() == nil {
			worker.logger.ErrorContext(ctx, "Control Plane maintenance pass failed", "error", err)
		}
		return
	}
	if report.Total() > 0 {
		worker.logger.InfoContext(ctx, "Control Plane maintenance removed expired records",
			"sessions", report.Sessions,
			"admin_sessions", report.AdminSessions,
			"oauth_sessions", report.OAuthSessions,
			"oauth_authorizations", report.OAuthAuthorizations,
			"oauth_browser_sessions", report.OAuthBrowserSessions,
			"idempotency_records", report.Idempotency,
		)
	}
}
