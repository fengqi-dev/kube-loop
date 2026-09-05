package preview

import (
	"context"
	"errors"
	"net"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/client/reverserelay"
	"github.com/fengqi-dev/kube-loop/internal/client/taskrelay"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
)

// gateway adapts the remote client to taskrelay.Gateway. The client names one
// method per task type, and the optional pause, resume, delete and list
// methods are reached through type assertions so an older client still works.
type gateway struct {
	client  Client
	streams TrafficStreamOpener
	dial    DialContextFunc
}

func (remoteGateway gateway) Pause(
	ctx context.Context, serverProfile profile.Profile, session remote.Session, taskID string,
) error {
	pauser, ok := remoteGateway.client.(interface {
		PausePreview(context.Context, profile.Profile, remote.Session, string) (remote.PreviewTask, error)
	})
	if !ok {
		_, err := remoteGateway.client.StopPreview(ctx, serverProfile, session, taskID)
		return err
	}
	_, err := pauser.PausePreview(ctx, serverProfile, session, taskID)
	return err
}

func (remoteGateway gateway) Resume(
	ctx context.Context, serverProfile profile.Profile, session remote.Session, taskID string,
) (taskrelay.Task, error) {
	lifecycle, ok := remoteGateway.client.(interface {
		ResumePreview(context.Context, profile.Profile, remote.Session, string) (remote.PreviewTask, error)
	})
	if !ok {
		return taskrelay.Task{}, errors.New("preview resume is unavailable")
	}
	task, err := lifecycle.ResumePreview(ctx, serverProfile, session, taskID)
	if err != nil {
		return taskrelay.Task{}, err
	}
	return relayTask(task, session), nil
}

func (remoteGateway gateway) Delete(
	ctx context.Context, serverProfile profile.Profile, session remote.Session, taskID string,
) error {
	lifecycle, ok := remoteGateway.client.(interface {
		DeletePreview(context.Context, profile.Profile, remote.Session, string) (remote.PreviewTask, error)
	})
	if !ok {
		_, err := remoteGateway.client.StopPreview(ctx, serverProfile, session, taskID)
		return err
	}
	_, err := lifecycle.DeletePreview(ctx, serverProfile, session, taskID)
	return err
}

// List reports only the Previews Restore can reconcile against: a task in a
// state the desktop may still own, carrying the local targets it needs.
func (remoteGateway gateway) List(
	ctx context.Context, serverProfile profile.Profile, session remote.Session,
) ([]taskrelay.Task, error) {
	lister, ok := remoteGateway.client.(interface {
		ListPreviews(context.Context, profile.Profile, remote.Session) ([]remote.PreviewTask, error)
	})
	if !ok {
		return nil, errors.New("preview restore is unavailable")
	}
	tasks, err := lister.ListPreviews(ctx, serverProfile, session)
	if err != nil {
		return nil, err
	}
	items := make([]taskrelay.Task, 0, len(tasks))
	for _, task := range tasks {
		reconcilable := task.State == remotetask.Stopped || task.State == remotetask.Pending ||
			task.State == remotetask.Running
		if !reconcilable || len(task.LocalTargets) == 0 {
			continue
		}
		targets, normalizeErr := reverserelay.NormalizeTargets(reverserelay.LocalTargets(task.LocalTargets), "preview")
		if normalizeErr != nil || matchTask(task, task.Name, targets) != nil {
			continue
		}
		item := relayTask(task, session)
		item.Targets = targets
		items = append(items, item)
	}
	return items, nil
}

// open dials the Data Plane Preview stream and wraps it in the reverse relay
// that serves the local targets.
func (remoteGateway gateway) open(
	ctx context.Context, serverProfile profile.Profile, task taskrelay.Task,
) (taskrelay.Relay, func() error, error) {
	connection, err := remoteGateway.streams.OpenTrafficStream(
		ctx, serverProfile.ID, tunnel.TrafficModePreview, task.ID,
	)
	if err != nil || connection == nil {
		if err == nil {
			err = errors.New("data Plane returned an empty Preview stream")
		}
		return nil, nil, err
	}
	return reverserelay.New(connection, task.Targets, remoteGateway.dial), connection.Close, nil
}

// relayTask normalizes one Preview onto the shared task shape. The Session
// carries the namespace, because the Gateway reports the task's own.
func relayTask(task remote.PreviewTask, session remote.Session) taskrelay.Task {
	return taskrelay.Task{
		ID: task.ID, SessionID: session.ID, Namespace: session.Namespace,
		Service: task.Name, ClusterIP: task.ClusterIP,
		Running: task.State == remotetask.Running,
	}
}

// confirm re-reads a resumed Preview once its relay is ready. The Gateway only
// publishes the Service, and so its ClusterIP, after the relay is up, and a
// Preview with no reachable address is worse than none at all.
func (remoteGateway gateway) confirm(
	ctx context.Context,
	serverProfile profile.Profile,
	session remote.Session,
	stored, current taskrelay.Task,
	reason taskrelay.Reason,
) (taskrelay.Task, error) {
	running := current
	if reason == taskrelay.Resumed {
		task, err := remoteGateway.client.GetPreview(ctx, serverProfile, session, stored.ID)
		if err != nil {
			return taskrelay.Task{}, err
		}
		running = relayTask(task, session)
	}
	if !running.Running || net.ParseIP(running.ClusterIP) == nil {
		return taskrelay.Task{}, errors.New("gateway returned an incomplete running Preview")
	}
	stored.ClusterIP, stored.Running = running.ClusterIP, true
	return stored, nil
}
