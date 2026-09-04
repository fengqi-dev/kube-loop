package preview

import (
	"context"
	"errors"
	"net"
	"strings"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/client/taskrelay"
	"github.com/fengqi-dev/kube-loop/internal/transport/trafficstream"
)

type Client interface {
	CreatePreview(
		context.Context,
		profile.Profile,
		remote.Session,
		remote.PreviewSpec,
		string,
	) (remote.PreviewTask, error)
	GetPreview(context.Context, profile.Profile, remote.Session, string) (remote.PreviewTask, error)
	StopPreview(context.Context, profile.Profile, remote.Session, string) (remote.PreviewTask, error)
}

type TrafficStreamOpener interface {
	OpenTrafficStream(context.Context, string, string, string) (*trafficstream.FrameConn, error)
}

var (
	ErrClosed = errors.New("preview manager is closed")
	// ErrNotManagedLocally reports a Preview this desktop never adopted.
	ErrNotManagedLocally = taskrelay.ErrNotManagedLocally
)

type Request struct {
	ProfileID string        `json:"profileId"`
	Namespace string        `json:"namespace"`
	Name      string        `json:"name"`
	Targets   []LocalTarget `json:"targets"`
}

type Info struct {
	ID        string        `json:"id"`
	ProfileID string        `json:"profileId"`
	SessionID string        `json:"sessionId"`
	Namespace string        `json:"namespace"`
	Name      string        `json:"name"`
	ClusterIP string        `json:"clusterIp"`
	State     string        `json:"state"`
	Targets   []LocalTarget `json:"targets"`
}

// Manager owns the desktop's Previews. Everything after a Preview has been
// created against the Gateway -- pause, resume, delete, listing and restoring
// -- is shared with Exchange and Mirror and lives in internal/client/taskrelay.
type Manager struct {
	*taskrelay.Manager[Info]

	client  Client
	gateway gateway
}

// describe renders one tracked Preview as the document the desktop shows. The
// shared entry calls the Kubernetes Service its Service; for a Preview that is
// the name the client asked for.
func describe(entry taskrelay.Entry) Info {
	return Info{
		ID: entry.Task.ID, ProfileID: entry.ProfileID, SessionID: entry.Task.SessionID,
		Namespace: entry.Task.Namespace, Name: entry.Task.Service,
		ClusterIP: entry.Task.ClusterIP, State: entry.State, Targets: entry.Task.Targets,
	}
}

func (manager *Manager) Start(
	ctx context.Context,
	serverProfile profile.Profile,
	session remote.Session,
	request Request,
) (Info, error) {
	release, closed := manager.Hold()
	defer release()
	if closed {
		return Info{}, ErrClosed
	}
	if ctx == nil || strings.TrimSpace(request.ProfileID) != serverProfile.ID ||
		session.State != previewSessionActive {
		return Info{}, errors.New("active Server Profile Session is required")
	}
	request.Namespace = strings.TrimSpace(request.Namespace)
	if request.Namespace == "" || request.Namespace != session.Namespace {
		return Info{}, errors.New("preview namespace must match the active Session namespace")
	}
	request.Name = strings.TrimSpace(request.Name)
	targets, ports, err := normalizeTargets(request.Targets)
	if err != nil {
		return Info{}, err
	}
	task, err := manager.client.CreatePreview(ctx, serverProfile, session, remote.PreviewSpec{
		Name: request.Name, Ports: ports, LocalTargets: remoteTargets(targets),
	}, "preview:"+uuid.NewString())
	if err != nil {
		return Info{}, err
	}
	discard := func(cause ...error) (Info, error) {
		return Info{}, errors.Join(append(
			cause, manager.gateway.Delete(ctx, serverProfile, session, task.ID),
		)...)
	}
	if err := matchTask(task, request.Name, targets); err != nil {
		return discard(err)
	}
	relayed := relayTask(task, session)
	relayed.Running, relayed.Targets = true, targets
	relay, closeStream, err := manager.gateway.open(ctx, serverProfile, relayed)
	if err != nil {
		return discard(err)
	}
	if err := relay.ReadReady(ctx); err != nil {
		_ = closeStream()
		return discard(err)
	}
	// The Gateway publishes the Preview Service, and so its ClusterIP, only
	// once the relay is ready, so the created task cannot carry it.
	running, err := manager.client.GetPreview(ctx, serverProfile, session, task.ID)
	if err == nil && (running.State != previewTaskRunning || net.ParseIP(running.ClusterIP) == nil) {
		err = errors.New("gateway returned an incomplete running Preview")
	}
	if err == nil {
		err = matchTask(running, request.Name, targets)
	}
	if err != nil {
		streamErr := relay.Stop(ctx)
		_ = closeStream()
		return discard(err, streamErr)
	}
	relayed.ClusterIP = running.ClusterIP
	info, err := manager.Adopt(ctx, serverProfile, session, relayed, relay)
	if err != nil {
		streamErr := relay.Stop(ctx)
		_ = closeStream()
		return discard(err, streamErr)
	}
	return info, nil
}
