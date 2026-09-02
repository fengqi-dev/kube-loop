package preview

import (
	"context"
	"errors"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

// Restore reconciles the local relay toward TrafficBinding state without
// writing remote desired state.
func (manager *Manager) Restore(
	ctx context.Context, serverProfile profile.Profile, session remote.Session,
) error {
	lister, ok := manager.client.(interface {
		ListPreviews(context.Context, profile.Profile, remote.Session) ([]remote.PreviewTask, error)
	})
	if !ok {
		return errors.New("preview restore is unavailable")
	}
	tasks, err := lister.ListPreviews(ctx, serverProfile, session)
	if err != nil {
		return err
	}
	manager.mu.Lock()
	for id, entry := range manager.active {
		if entry.profile.ID == serverProfile.ID && entry.session.ID != session.ID {
			if entry.cancel != nil {
				entry.cancel()
			}
			delete(manager.active, id)
		}
	}
	manager.mu.Unlock()
	for _, task := range tasks {
		if (task.State != remotetask.Stopped && task.State != remotetask.Pending &&
			task.State != remotetask.Running) || len(task.LocalTargets) == 0 {
			continue
		}
		manager.mu.Lock()
		entry := manager.active[task.ID]
		if entry != nil {
			entry.profile, entry.session, entry.task = serverProfile, session, task
			localState := entry.info.State
			manager.mu.Unlock()
			if task.State == remotetask.Running && localState == previewStatePaused {
				if _, err := manager.startLocal(ctx, entry, task, false, false); err != nil {
					return err
				}
			} else if task.State != remotetask.Running && localState == previewTaskRunning {
				if err := manager.stopLocal(ctx, entry); err != nil {
					return err
				}
			}
			continue
		}
		targets, _, normalizeErr := normalizeTargets(previewTargets(task.LocalTargets))
		if normalizeErr != nil || matchTask(task, task.Name, targets) != nil {
			manager.mu.Unlock()
			continue
		}
		entry = &activePreview{
			profile: serverProfile, session: session, task: task,
			info: Info{
				ID: task.ID, ProfileID: serverProfile.ID, SessionID: session.ID,
				Namespace: session.Namespace, Name: task.Name, ClusterIP: task.ClusterIP,
				State: previewStatePaused, Targets: targets,
			},
		}
		manager.active[task.ID] = entry
		manager.mu.Unlock()
		if task.State == remotetask.Running {
			if _, err := manager.startLocal(ctx, entry, task, false, false); err != nil {
				manager.mu.Lock()
				if manager.active[task.ID] == entry {
					delete(manager.active, task.ID)
				}
				manager.mu.Unlock()
				return err
			}
		}
	}
	return nil
}

func previewTargets(items []remote.LocalTarget) []LocalTarget {
	targets := make([]LocalTarget, len(items))
	for index, item := range items {
		targets[index] = LocalTarget{
			Protocol: item.Protocol, ServicePort: item.ServicePort,
			LocalHost: item.LocalHost, LocalPort: item.LocalPort,
		}
	}
	return targets
}
