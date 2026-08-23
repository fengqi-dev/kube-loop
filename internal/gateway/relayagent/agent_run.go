package relayagent

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"
)

func (agent *Agent) Start(ctx context.Context) error {
	if agent == nil || ctx == nil {
		return errors.New("relay agent context is required")
	}
	runContext, cancel := context.WithCancel(ctx)
	agent.mu.Lock()
	if agent.lifecycle != lifecycleIdle {
		agent.mu.Unlock()
		cancel()
		return errors.New("relay agent is already starting or started")
	}
	agent.lifecycle = lifecycleStarting
	agent.cancel = cancel
	agent.mu.Unlock()
	running := false
	defer func() {
		if running {
			return
		}
		cancel()
		agent.mu.Lock()
		agent.lifecycle = lifecycleIdle
		agent.cancel = nil
		agent.mu.Unlock()
	}()
	var err error
	for attempt := 1; attempt <= agent.config.RegistrationAttempts; attempt++ {
		err = agent.register(runContext)
		if err == nil {
			break
		}
		if !retryableRegistrationError(err) || attempt == agent.config.RegistrationAttempts {
			return err
		}
		agent.logf(
			"Relay registration failed (attempt %d/%d); retrying in %s: %v",
			attempt, agent.config.RegistrationAttempts, agent.config.RegistrationRetryDelay, err,
		)
		if !waitForRetry(runContext, agent.config.RegistrationRetryDelay) {
			return runContext.Err()
		}
	}
	if err := runContext.Err(); err != nil {
		return err
	}
	agent.mu.Lock()
	agent.lifecycle = lifecycleRunning
	agent.mu.Unlock()
	go agent.run(runContext)
	running = true
	return nil
}

func retryableRegistrationError(err error) bool {
	if httpError, ok := errors.AsType[*HTTPError](err); ok {
		return httpError.Status == http.StatusTooManyRequests || httpError.Status >= http.StatusInternalServerError
	}
	var requestError *url.Error
	return errors.As(err, &requestError)
}

func (agent *Agent) run(ctx context.Context) {
	defer func() {
		agent.mu.Lock()
		agent.lifecycle = lifecycleStopped
		agent.cancel = nil
		agent.mu.Unlock()
		close(agent.done)
	}()
	for {
		agent.mu.RLock()
		after := agent.heartbeatAfter
		agent.mu.RUnlock()
		timer := time.NewTimer(after)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if err := agent.heartbeat(ctx); err != nil {
			agent.setError(err)
			agent.logf("Relay heartbeat failed: %v", err)
			if isLeaseError(err) {
				registerErr := agent.register(ctx)
				if registerErr == nil {
					continue
				}
				agent.setError(registerErr)
				agent.logf("Relay re-registration failed: %v", registerErr)
			}
			if !waitForRetry(ctx, time.Second) {
				return
			}
		}
	}
}

func waitForRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
