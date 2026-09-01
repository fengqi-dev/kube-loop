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

// Restore repopulates locally resumable Port Forward sessions from the
// TrafficBindings owned by the active remote Session.
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
	defer manager.mu.Unlock()
	for id, entry := range manager.active {
		if entry.profile.ID == serverProfile.ID && entry.session.ID != session.ID {
			delete(manager.active, id)
		}
	}
	for _, task := range tasks {
		_, wasDeleted := manager.deleted[task.ID]
		if _, exists := manager.active[task.ID]; exists || wasDeleted || task.LocalPort == 0 ||
			(task.State != remotetask.Stopped && task.State != remotetask.Pending) {
			continue
		}
		manager.active[task.ID] = &activeForward{
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
	}
	return nil
}
