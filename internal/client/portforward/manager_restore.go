package portforward

import (
	"context"
	"errors"
	"net"
	"strconv"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

// Restore reconciles local listeners toward the state projected from
// TrafficBinding CRDs. It never changes the remote desired state.
func (manager *Manager) Restore(
	ctx context.Context, serverProfile profile.Profile, session remote.Session,
) error {
	lister, ok := manager.client.(interface {
		ListPortForwards(context.Context, profile.Profile, remote.Session) ([]remote.PortForwardTask, error)
	})
	if !ok {
		return errors.New("port Forward restore is unavailable")
	}
	tasks, err := lister.ListPortForwards(ctx, serverProfile, session)
	if err != nil {
		return err
	}
	manager.mu.Lock()
	for id, entry := range manager.active {
		if entry.profile.ID == serverProfile.ID && entry.session.ID != session.ID {
			if entry.localID != "" {
				_ = manager.locals.Stop(entry.localID)
			}
			delete(manager.active, id)
		}
	}
	manager.mu.Unlock()
	for _, task := range tasks {
		if task.LocalPort == 0 ||
			(task.State != remotetask.Stopped && task.State != remotetask.Pending &&
				task.State != remotetask.Running) {
			continue
		}
		manager.mu.Lock()
		_, wasDeleted := manager.deleted[task.ID]
		entry := manager.active[task.ID]
		if wasDeleted {
			manager.mu.Unlock()
			continue
		}
		if entry != nil {
			entry.profile, entry.session, entry.task = serverProfile, session, task
			if task.State == remotetask.Running && entry.localID == "" {
				manager.mu.Unlock()
				if _, err := manager.startLocal(ctx, entry, task, false); err != nil {
					return err
				}
				continue
			}
			if task.State != remotetask.Running && entry.localID != "" {
				localID := entry.localID
				entry.localID = ""
				entry.info.State = portForwardStatePaused
				manager.mu.Unlock()
				if err := manager.locals.Stop(localID); err != nil {
					return err
				}
				continue
			}
			manager.mu.Unlock()
			continue
		}
		entry = &activeForward{
			profile: serverProfile,
			session: session,
			task:    task,
			info: Info{
				ID: task.ID, ProfileID: serverProfile.ID, SessionID: session.ID,
				Namespace: session.Namespace, Kind: task.Kind, Name: task.Name,
				Protocol: task.Protocol, RemotePort: task.RemotePort, LocalPort: task.LocalPort,
				Address:     net.JoinHostPort("127.0.0.1", strconv.Itoa(int(task.LocalPort))),
				DialAddress: task.DialAddress, State: portForwardStatePaused,
			},
		}
		manager.active[task.ID] = entry
		manager.mu.Unlock()
		if task.State == remotetask.Running {
			if _, err := manager.startLocal(ctx, entry, task, false); err != nil {
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
