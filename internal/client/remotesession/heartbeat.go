package remotesession

import (
	"context"
	"errors"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/remote"
)

func (manager *Manager) SessionUpdates() <-chan remote.SessionUpdate {
	return manager.updates
}

// Refresh performs an immediate heartbeat for Data Plane recovery. It returns
// the authoritative generation and prevents a stale reconnect from replacing a
// newer Session selected by the desktop.
func (manager *Manager) Refresh(ctx context.Context, profileID string) (remote.Session, error) {
	manager.lifecycle.RLock()
	defer manager.lifecycle.RUnlock()
	operation := manager.profileOperation(profileID)
	operation.Lock()
	defer operation.Unlock()
	return manager.refresh(ctx, profileID)
}

func (manager *Manager) refresh(ctx context.Context, profileID string) (remote.Session, error) {
	manager.mu.Lock()
	current, ok := manager.active[profileID]
	manager.mu.Unlock()
	if !ok {
		return remote.Session{}, errors.New("remote Session is not connected")
	}
	next, err := manager.gateway.HeartbeatSession(ctx, current.profile, current.session)
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if err != nil {
		current.lastError = err
		manager.active[profileID] = current
		return current.session, err
	}
	current.session = next
	current.lastError = nil
	manager.active[profileID] = current
	manager.publishSessionUpdateLocked(profileID, next)
	return next, nil
}

func (manager *Manager) heartbeatLoop() {
	defer close(manager.done)
	ticker := time.NewTicker(manager.interval)
	defer ticker.Stop()
	for {
		select {
		case <-manager.ctx.Done():
			return
		case <-ticker.C:
			manager.heartbeat()
		}
	}
}

func (manager *Manager) heartbeat() {
	manager.lifecycle.RLock()
	defer manager.lifecycle.RUnlock()
	manager.mu.Lock()
	profileIDs := make([]string, 0, len(manager.active))
	for profileID := range manager.active {
		profileIDs = append(profileIDs, profileID)
	}
	manager.mu.Unlock()
	for _, profileID := range profileIDs {
		operation := manager.profileOperation(profileID)
		operation.Lock()
		ctx, cancel := context.WithTimeout(manager.ctx, manager.interval)
		_, _ = manager.refresh(ctx, profileID)
		cancel()
		operation.Unlock()
	}
}

func (manager *Manager) publishSessionUpdateLocked(profileID string, session remote.Session) {
	update := remote.SessionUpdate{ProfileID: profileID, Session: session}
	select {
	case manager.updates <- update:
		return
	default:
	}
	select {
	case <-manager.updates:
	default:
	}
	select {
	case manager.updates <- update:
	default:
	}
}
