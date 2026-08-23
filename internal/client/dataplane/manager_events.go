package dataplane

import (
	"errors"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/client/remote"
)

func (manager *Manager) emit(profileID string, status Status, err error) {
	if manager.config.OnStatus == nil {
		return
	}
	event := StatusEvent{ProfileID: profileID, Status: status}
	if err != nil {
		event.Error = err.Error()
		switch {
		case errors.Is(err, errSystemResumed):
			event.Reason = reasonSystemResumed
			event.Retryable = true
		case errors.Is(err, errNetworkSpecChanged):
			event.Reason = reasonNetworkSpecChanged
			event.Retryable = true
		case errors.Is(err, errSessionChanged):
			event.Reason = reasonSessionChanged
			event.Retryable = true
		}
		switch status.State {
		case dataplaneReconnecting:
			if event.Reason == "" {
				event.Reason = reasonTransportInterrupted
				event.Retryable = true
			}
		case dataplaneError:
			event.Reason, event.Retryable = recoveryFailureAction(err)
		}
	}
	select {
	case manager.events <- event:
		return
	case <-manager.ctx.Done():
		return
	default:
	}
	// A UI callback is outside the Data Plane trust boundary and may stall.
	// Preserve lifecycle progress by coalescing a full queue toward its newest
	// status instead of blocking while a Manager mutex may be held.
	select {
	case <-manager.events:
	default:
	}
	select {
	case manager.events <- event:
	case <-manager.ctx.Done():
	default:
	}
}

func recoveryFailureAction(err error) (string, bool) {
	if apiError, ok := errors.AsType[*remote.APIError](err); ok {
		switch apiError.Status {
		case 401:
			return reasonAuthenticationRequired, false
		case 403:
			return reasonAccessDenied, false
		case 404:
			return reasonSessionExpired, true
		}
	}
	message := err.Error()
	if strings.Contains(message, "Session identity changed") || strings.Contains(message, "stale Session generation") {
		return reasonSessionChanged, true
	}
	return reasonNetworkUnavailable, true
}

func (manager *Manager) eventLoop(callback func(StatusEvent)) {
	for {
		select {
		case event := <-manager.events:
			callback(event)
		case <-manager.ctx.Done():
			return
		}
	}
}
