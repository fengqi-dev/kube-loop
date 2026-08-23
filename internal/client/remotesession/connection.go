package remotesession

import (
	"context"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
)

func (manager *Manager) Connect(
	ctx context.Context,
	serverProfile profile.Profile,
	namespace string,
) (remote.Session, error) {
	if manager.closed.Load() {
		return remote.Session{}, ErrClosed
	}
	manager.lifecycle.RLock()
	defer manager.lifecycle.RUnlock()
	if manager.closed.Load() {
		return remote.Session{}, ErrClosed
	}
	operation := manager.profileOperation(serverProfile.ID)
	operation.Lock()
	defer operation.Unlock()

	manager.mu.Lock()
	current, ok := manager.active[serverProfile.ID]
	manager.mu.Unlock()
	if ok {
		if current.session.Namespace == namespace && current.session.State == remoteSessionActive {
			if current.lastError == nil {
				return current.session, nil
			}
			if !isGone(current.lastError) {
				return current.session, current.lastError
			}
			manager.mu.Lock()
			delete(manager.active, serverProfile.ID)
			manager.mu.Unlock()
		} else {
			if _, err := manager.gateway.DisconnectSession(
				ctx,
				current.profile,
				current.session,
			); err != nil &&
				!isGone(err) {
				return remote.Session{}, err
			}
			manager.mu.Lock()
			delete(manager.active, serverProfile.ID)
			manager.mu.Unlock()
		}
	}
	pendingID := serverProfile.ID + "\x00" + namespace
	manager.mu.Lock()
	idempotencyKey := manager.pendingKeys[pendingID]
	if idempotencyKey == "" {
		idempotencyKey = "desktop-" + uuid.NewString()
		manager.pendingKeys[pendingID] = idempotencyKey
	}
	manager.mu.Unlock()
	session, err := manager.gateway.CreateSession(ctx, serverProfile, namespace, idempotencyKey)
	if err != nil {
		return remote.Session{}, err
	}
	manager.mu.Lock()
	delete(manager.pendingKeys, pendingID)
	manager.active[serverProfile.ID] = entry{profile: serverProfile, session: session}
	manager.mu.Unlock()
	return session, nil
}

func (manager *Manager) Disconnect(ctx context.Context, profileID string) error {
	manager.lifecycle.RLock()
	defer manager.lifecycle.RUnlock()
	operation := manager.profileOperation(profileID)
	operation.Lock()
	defer operation.Unlock()

	manager.mu.Lock()
	current, ok := manager.active[profileID]
	manager.mu.Unlock()
	if !ok {
		return nil
	}
	_, err := manager.gateway.DisconnectSession(ctx, current.profile, current.session)
	if err != nil && !isGone(err) {
		return err
	}
	manager.mu.Lock()
	delete(manager.active, profileID)
	manager.mu.Unlock()
	return nil
}
