package remotesession

import (
	"context"
	"errors"
	"maps"
)

func (manager *Manager) Shutdown(ctx context.Context) error {
	manager.cancel()
	select {
	case <-manager.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	manager.lifecycle.Lock()
	defer manager.lifecycle.Unlock()
	manager.mu.Lock()
	active := maps.Clone(manager.active)
	manager.mu.Unlock()
	var result error
	for profileID, current := range active {
		if _, err := manager.gateway.DisconnectSession(
			ctx,
			current.profile,
			current.session,
		); err != nil &&
			!isGone(err) {
			result = errors.Join(result, err)
			continue
		}
		manager.mu.Lock()
		delete(manager.active, profileID)
		manager.mu.Unlock()
	}
	return result
}
