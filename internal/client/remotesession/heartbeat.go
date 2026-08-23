package remotesession

import (
	"context"
	"errors"
	"sync"
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
	if manager.closed.Load() {
		return remote.Session{}, ErrClosed
	}
	manager.lifecycle.RLock()
	defer manager.lifecycle.RUnlock()
	if manager.closed.Load() {
		return remote.Session{}, ErrClosed
	}
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
	if manager.closed.Load() {
		return
	}
	manager.lifecycle.RLock()
	defer manager.lifecycle.RUnlock()
	manager.mu.Lock()
	profileIDs := make([]string, 0, len(manager.active))
	for profileID := range manager.active {
		profileIDs = append(profileIDs, profileID)
	}
	manager.mu.Unlock()
	var heartbeats sync.WaitGroup
	for _, profileID := range profileIDs {
		heartbeats.Go(func() {
			operation := manager.profileOperation(profileID)
			operation.Lock()
			defer operation.Unlock()
			ctx, cancel := context.WithTimeout(manager.ctx, manager.interval)
			defer cancel()
			_, _ = manager.refresh(ctx, profileID)
		})
	}
	heartbeats.Wait()
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
