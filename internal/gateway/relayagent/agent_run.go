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
	agent.mu.Lock()
	if agent.started {
		agent.mu.Unlock()
		return errors.New("relay agent is already started")
	}
	agent.mu.Unlock()
	var err error
	for attempt := 1; attempt <= agent.config.RegistrationAttempts; attempt++ {
		err = agent.register(ctx)
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
		timer := time.NewTimer(agent.config.RegistrationRetryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	runContext, cancel := context.WithCancel(ctx)
	agent.mu.Lock()
	agent.started = true
	agent.cancel = cancel
	agent.mu.Unlock()
	go agent.run(runContext)
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
	defer close(agent.done)
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
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
	}
}
