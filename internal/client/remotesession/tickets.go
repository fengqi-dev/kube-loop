package remotesession

import (
	"context"
	"errors"

	"github.com/fengqi-dev/kube-loop/internal/client/remote"
)

func (manager *Manager) IssueRelayTicket(
	ctx context.Context,
	profileID string,
) (remote.RelayTicket, error) {
	manager.mu.Lock()
	current, ok := manager.active[profileID]
	manager.mu.Unlock()
	if !ok {
		return remote.RelayTicket{}, errors.New("remote Session is not connected")
	}
	if current.lastError != nil {
		return remote.RelayTicket{}, current.lastError
	}
	return manager.gateway.IssueRelayTicket(ctx, current.profile, current.session)
}

func (manager *Manager) RelayTicketSource(profileID string) func(context.Context) (remote.RelayTicket, error) {
	manager.mu.Lock()
	bound, ok := manager.active[profileID]
	manager.mu.Unlock()
	return func(ctx context.Context) (remote.RelayTicket, error) {
		manager.mu.Lock()
		current, active := manager.active[profileID]
		if !ok || !active || current.session.ID != bound.session.ID ||
			current.session.Generation != bound.session.Generation {
			manager.mu.Unlock()
			return remote.RelayTicket{}, errors.New("remote Session generation changed")
		}
		if current.lastError != nil {
			err := current.lastError
			manager.mu.Unlock()
			return remote.RelayTicket{}, err
		}
		manager.mu.Unlock()
		return manager.gateway.IssueRelayTicket(ctx, current.profile, current.session)
	}
}
