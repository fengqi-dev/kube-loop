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
		if task.State != remotetask.Stopped && task.State != remotetask.Pending &&
			task.State != remotetask.Running {
			continue
		}
		manager.mu.Lock()
		entry := manager.active[task.ID]
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
		// An entry that only exists on the Gateway needs a persisted local port
		// to be re-materialized; desktop-side allocations (requested as 0) are
		// not recoverable directly, so the entry stays available as paused with a
		// zero port and is re-bound on Resume (which re-allocates the local port).
		entry = &activeForward{
			profile: serverProfile,
			session: session,
			task:    task,
			info: Info{
				ID: task.ID, ProfileID: serverProfile.ID, SessionID: session.ID,
				Namespace: session.Namespace, Kind: task.Kind, Name: task.Name,
				Protocol: task.Protocol, RemotePort: task.RemotePort, LocalPort: task.LocalPort,
				Address:     portForwardAddress(task.LocalPort),
				DialAddress: task.DialAddress, State: portForwardStatePaused,
			},
		}
		manager.active[task.ID] = entry
		manager.mu.Unlock()
		if task.State == remotetask.Running && task.LocalPort != 0 {
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

func portForwardAddress(port uint16) string {
	if port == 0 {
		return ""
	}
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port)))
}
