package exchange

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/client/taskrelay"
	"github.com/fengqi-dev/kube-loop/internal/transport/trafficstream"
)

type Client interface {
	CreateExchange(
		context.Context,
		profile.Profile,
		remote.Session,
		remote.ExchangeSpec,
		string,
	) (remote.ExchangeTask, error)
	StopExchange(context.Context, profile.Profile, remote.Session, string) (remote.ExchangeTask, error)
}

type TrafficStreamOpener interface {
	OpenTrafficStream(context.Context, string, string, string) (*trafficstream.FrameConn, error)
}

var (
	ErrClosed = errors.New("exchange manager is closed")
	// ErrNotManagedLocally reports an Exchange this desktop never adopted.
	ErrNotManagedLocally = taskrelay.ErrNotManagedLocally
)

type Request struct {
	ProfileID string        `json:"profileId"`
	Service   string        `json:"service"`
	Targets   []LocalTarget `json:"targets"`
}

type Info struct {
	ID        string        `json:"id"`
	ProfileID string        `json:"profileId"`
	SessionID string        `json:"sessionId"`
	Namespace string        `json:"namespace"`
	Service   string        `json:"service"`
	ClusterIP string        `json:"clusterIp"`
	State     string        `json:"state"`
	Targets   []LocalTarget `json:"targets"`
}

// Manager owns the desktop's Exchanges. Everything after an Exchange has been
// created against the Gateway -- pause, resume, delete, listing and restoring
// -- is shared with Mirror and Preview and lives in internal/client/taskrelay.
type Manager struct {
	*taskrelay.Manager[Info]

	client  Client
	gateway gateway
}

// describe renders one tracked Exchange as the document the desktop shows.
func describe(entry taskrelay.Entry) Info {
	return Info{
		ID: entry.Task.ID, ProfileID: entry.ProfileID, SessionID: entry.Task.SessionID,
		Namespace: entry.Task.Namespace, Service: entry.Task.Service,
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
		session.State != exchangeSessionActive {
		return Info{}, errors.New("active Server Profile Session is required")
	}
	targets, ports, err := normalizeTargets(request.Targets)
	if err != nil {
		return Info{}, err
	}
	task, err := manager.client.CreateExchange(ctx, serverProfile, session, remote.ExchangeSpec{
		Service: strings.TrimSpace(request.Service), Ports: ports, LocalTargets: remoteTargets(targets),
	}, "exchange:"+uuid.NewString())
	if err != nil {
		return Info{}, err
	}
	discard := func(cause error) (Info, error) {
		return Info{}, errors.Join(
			cause, manager.gateway.Delete(ctx, serverProfile, session, task.ID),
		)
	}
	if err := matchTaskTargets(task, targets); err != nil {
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
	info, err := manager.Adopt(ctx, serverProfile, session, relayed, relay)
	if err != nil {
		_ = closeStream()
		return discard(err)
	}
	return info, nil
}
