package mirror

import (
	"context"
	"errors"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
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
	config  Config
}

func (remoteGateway gateway) Pause(
	ctx context.Context, serverProfile profile.Profile, session remote.Session, taskID string,
) error {
	pauser, ok := remoteGateway.client.(interface {
		PauseMirror(context.Context, profile.Profile, remote.Session, string) (remote.MirrorTask, error)
	})
	if !ok {
		_, err := remoteGateway.client.StopMirror(ctx, serverProfile, session, taskID)
		return err
	}
	_, err := pauser.PauseMirror(ctx, serverProfile, session, taskID)
	return err
}

func (remoteGateway gateway) Resume(
	ctx context.Context, serverProfile profile.Profile, session remote.Session, taskID string,
) (taskrelay.Task, error) {
	lifecycle, ok := remoteGateway.client.(interface {
		ResumeMirror(context.Context, profile.Profile, remote.Session, string) (remote.MirrorTask, error)
	})
	if !ok {
		return taskrelay.Task{}, errors.New("mirror resume is unavailable")
	}
	task, err := lifecycle.ResumeMirror(ctx, serverProfile, session, taskID)
	if err != nil {
		return taskrelay.Task{}, err
	}
	return relayTask(task, session), nil
}

func (remoteGateway gateway) Delete(
	ctx context.Context, serverProfile profile.Profile, session remote.Session, taskID string,
) error {
	lifecycle, ok := remoteGateway.client.(interface {
		DeleteMirror(context.Context, profile.Profile, remote.Session, string) (remote.MirrorTask, error)
	})
	if !ok {
		_, err := remoteGateway.client.StopMirror(ctx, serverProfile, session, taskID)
		return err
	}
	_, err := lifecycle.DeleteMirror(ctx, serverProfile, session, taskID)
	return err
}

// List reports only the Mirrors Restore can reconcile against: a task in a
// state the desktop may still own, carrying the local targets it needs.
func (remoteGateway gateway) List(
	ctx context.Context, serverProfile profile.Profile, session remote.Session,
) ([]taskrelay.Task, error) {
	lister, ok := remoteGateway.client.(interface {
		ListMirrors(context.Context, profile.Profile, remote.Session) ([]remote.MirrorTask, error)
	})
	if !ok {
		return nil, errors.New("mirror restore is unavailable")
	}
	tasks, err := lister.ListMirrors(ctx, serverProfile, session)
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
		targets, _, normalizeErr := normalizeTargets(mirrorTargets(task.LocalTargets))
		if normalizeErr != nil || matchTaskTargets(task, targets) != nil {
			continue
		}
		item := relayTask(task, session)
		item.Targets = targets
		items = append(items, item)
	}
	return items, nil
}

// open dials the Data Plane Mirror stream and wraps it in the shadow relay
// that copies traffic to the local targets.
func (remoteGateway gateway) open(
	ctx context.Context, serverProfile profile.Profile, task taskrelay.Task,
) (taskrelay.Relay, func() error, error) {
	connection, err := remoteGateway.streams.OpenTrafficStream(
		ctx, serverProfile.ID, tunnel.TrafficModeMirror, task.ID,
	)
	if err != nil || connection == nil {
		if err == nil {
			err = errors.New("data Plane returned an empty Mirror stream")
		}
		return nil, nil, err
	}
	relay := newLocalRelay(connection, task.Targets, remoteGateway.dial, remoteGateway.config)
	return shadowRelay{relay}, connection.Close, nil
}

// relayTask normalizes one Mirror onto the shared task shape. The Session
// carries the namespace, because the Gateway reports the task's own.
func relayTask(task remote.MirrorTask, session remote.Session) taskrelay.Task {
	return taskrelay.Task{
		ID: task.ID, SessionID: session.ID, Namespace: session.Namespace,
		Service: task.Service, ClusterIP: task.ClusterIP,
		Running: task.State == remotetask.Running,
	}
}

// shadowRelay exposes the Mirror shadow relay through taskrelay.Relay. Its own
// methods stay unexported: nothing outside this package drives it directly.
type shadowRelay struct{ *localRelay }

func (relay shadowRelay) ReadReady(ctx context.Context) error { return relay.readReady(ctx) }

func (relay shadowRelay) Run(ctx context.Context) error { return relay.run(ctx) }

func (relay shadowRelay) Stop(ctx context.Context) error { return relay.stop(ctx) }
