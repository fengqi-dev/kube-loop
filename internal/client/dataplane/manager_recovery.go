package dataplane

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/remote"
)

func (manager *Manager) watch(
	profileID string,
	entry *managedRuntime,
	runtime *Runtime,
	transportDone <-chan struct{},
) {
	select {
	case <-transportDone:
	case <-runtime.Done():
		return
	case <-manager.ctx.Done():
		return
	}
	select {
	case <-runtime.Done():
		manager.mu.Lock()
		if manager.active[profileID] == entry && entry.runtime == runtime {
			entry.lastError = runtime.Err()
		}
		manager.mu.Unlock()
		return
	default:
	}
	manager.mu.Lock()
	if manager.active[profileID] != entry || entry.runtime != runtime || manager.ctx.Err() != nil ||
		runtime.TransportDone() != transportDone {
		manager.mu.Unlock()
		return
	}
	if entry.recovering {
		manager.mu.Unlock()
		return
	}
	entry.recovering = true
	entry.lastError = runtime.TransportErr()
	baseline := entry.session
	manager.mu.Unlock()
	status := runtime.Status()
	status.State = dataplaneReconnecting
	manager.emit(profileID, status, entry.lastError)
	manager.recover(profileID, entry, runtime, baseline)
}

// syncSession applies authoritative heartbeat updates while the transport is
// healthy. Every generation owns a matching RelayTicket, control token and WSS
// pool, so advancing a generation requires one atomic transport replacement.
// A changed NetworkSpec additionally reinstalls TUN after the replacement is
// ready; both paths preserve the stable local SOCKS address.
func (manager *Manager) syncSession(update remote.SessionUpdate) {
	profileID := strings.TrimSpace(update.ProfileID)
	session := update.Session
	manager.mu.Lock()
	entry := manager.active[profileID]
	if profileID == "" || entry == nil || entry.runtime == nil || entry.recovering || manager.ctx.Err() != nil {
		manager.mu.Unlock()
		return
	}
	runtime := entry.runtime
	if err := validateRecoverySession(entry.session, session); err != nil {
		manager.mu.Unlock()
		return
	}
	if session.Generation == entry.session.Generation && session.NetworkSpecHash == entry.session.NetworkSpecHash {
		manager.mu.Unlock()
		return
	}
	entry.recovering = true
	baseline := entry.session
	reason := errSessionChanged
	if session.NetworkSpecHash != entry.session.NetworkSpecHash {
		reason = errNetworkSpecChanged
	}
	manager.mu.Unlock()
	status := runtime.Status()
	status.State = dataplaneReconnecting
	manager.emit(profileID, status, reason)
	go manager.recover(profileID, entry, runtime, baseline)
}

func (manager *Manager) sessionUpdateLoop(updates <-chan remote.SessionUpdate) {
	for {
		select {
		case update, ok := <-updates:
			if !ok {
				return
			}
			manager.syncSession(update)
		case <-manager.ctx.Done():
			return
		}
	}
}

func (manager *Manager) recover(profileID string, entry *managedRuntime, failed *Runtime, baseline remote.Session) {
	var lastError error
	for attempt := range manager.config.RecoveryAttempts {
		if attempt > 0 && !manager.waitRecovery(attempt) {
			return
		}
		session, err := manager.sessions.Refresh(manager.ctx, profileID)
		if err == nil {
			err = validateRecoverySession(baseline, session)
		}
		if err != nil {
			lastError = err
			if unrecoverableSessionError(err) {
				break
			}
			continue
		}
		manager.mu.Lock()
		valid := manager.active[profileID] == entry && entry.runtime == failed && manager.ctx.Err() == nil
		manager.mu.Unlock()
		if !valid {
			return
		}
		err = failed.Reconnect(manager.ctx, entry.profile, session, manager.sessions.RelayTicketSource(profileID))
		if err != nil {
			lastError = err
			continue
		}
		manager.mu.Lock()
		if manager.active[profileID] != entry || entry.runtime != failed || manager.ctx.Err() != nil ||
			session.Generation < entry.session.Generation {
			manager.mu.Unlock()
			return
		}
		entry.session = session
		entry.recovering = false
		entry.lastError = nil
		manager.mu.Unlock()
		manager.emit(profileID, failed.Status(), nil)
		go manager.watch(profileID, entry, failed, failed.TransportDone())
		return
	}
	if lastError == nil {
		lastError = errors.New("data Plane recovery attempts exhausted")
	}
	manager.mu.Lock()
	shouldClose := manager.active[profileID] == entry && entry.runtime == failed
	manager.mu.Unlock()
	if shouldClose {
		closeError := failed.Close()
		terminalError := fmt.Errorf("recover Data Plane: %w", lastError)
		if closeError != nil {
			terminalError = errors.Join(terminalError, fmt.Errorf("close failed Data Plane: %w", closeError))
		}
		manager.mu.Lock()
		stillActive := manager.active[profileID] == entry && entry.runtime == failed
		if stillActive {
			entry.recovering = false
			entry.lastError = terminalError
		}
		manager.mu.Unlock()
		if !stillActive {
			return
		}
		status := failed.Status()
		status.State = dataplaneError
		manager.emit(profileID, status, terminalError)
	}
}
func (manager *Manager) waitRecovery(attempt int) bool {
	delay := manager.config.RecoveryBackoff
	for step := 1; step < attempt && delay < 30*time.Second; step++ {
		delay *= 2
		if delay > 30*time.Second {
			delay = 30 * time.Second
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-manager.ctx.Done():
		return false
	}
}

func validateRecoverySession(previous, current remote.Session) error {
	if current.State != dataplaneSessionActive || current.ID != previous.ID {
		return errors.New("active Session identity changed during Data Plane recovery")
	}
	if current.Generation < previous.Generation {
		return errors.New("stale Session generation returned during Data Plane recovery")
	}
	return nil
}

func unrecoverableSessionError(err error) bool {
	if apiError, ok := errors.AsType[*remote.APIError](err); ok {
		return apiError.Status == 401 || apiError.Status == 403 || apiError.Status == 404
	}
	return false
}
