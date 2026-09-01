package exchange

import (
	"context"
	"errors"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

// Restore repopulates locally resumable tasks from the active remote Session.
func (manager *Manager) Restore(
	ctx context.Context, serverProfile profile.Profile, session remote.Session,
) error {
	lister, ok := manager.client.(interface {
		ListExchanges(context.Context, profile.Profile, remote.Session) ([]remote.ExchangeTask, error)
	})
	if !ok {
		return errors.New("exchange restore is unavailable")
	}
	tasks, err := lister.ListExchanges(ctx, serverProfile, session)
	if err != nil {
		return err
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	for id, entry := range manager.active {
		if entry.profile.ID == serverProfile.ID && entry.session.ID != session.ID {
			delete(manager.active, id)
		}
	}
	for _, task := range tasks {
		_, wasDeleted := manager.deleted[task.ID]
		if _, exists := manager.active[task.ID]; exists || wasDeleted ||
			(task.State != remotetask.Stopped && task.State != remotetask.Pending) ||
			len(task.LocalTargets) == 0 {
			continue
		}
		targets, _, normalizeErr := normalizeTargets(exchangeTargets(task.LocalTargets))
		if normalizeErr != nil || matchTaskTargets(task, targets) != nil {
			continue
		}
		manager.active[task.ID] = &activeExchange{
			profile: serverProfile, session: session, task: task,
			info: Info{
				ID: task.ID, ProfileID: serverProfile.ID, SessionID: session.ID,
				Namespace: session.Namespace, Service: task.Service, ClusterIP: task.ClusterIP,
				State: exchangeStatePaused, Targets: targets,
			},
		}
	}
	return nil
}

func exchangeTargets(items []remote.LocalTarget) []LocalTarget {
	targets := make([]LocalTarget, len(items))
	for index, item := range items {
		targets[index] = LocalTarget{
			Protocol: item.Protocol, ServicePort: item.ServicePort,
			LocalHost: item.LocalHost, LocalPort: item.LocalPort,
		}
	}
	return targets
}
