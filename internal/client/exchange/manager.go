package exchange

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/client/reverserelay"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficstream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
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

type activeExchange struct {
	profile profile.Profile
	session remote.Session
	task    remote.ExchangeTask
	relay   *reverserelay.Relay
	cancel  context.CancelFunc
	done    chan struct{}
	info    Info
}

type Manager struct {
	client  Client
	streams TrafficStreamOpener
	dial    DialContextFunc

	mu     sync.Mutex
	active map[string]*activeExchange
}

func (manager *Manager) Start(
	ctx context.Context,
	serverProfile profile.Profile,
	session remote.Session,
	request Request,
) (Info, error) {
	if ctx == nil || strings.TrimSpace(request.ProfileID) != serverProfile.ID ||
		session.State != exchangeSessionActive {
		return Info{}, errors.New("active Server Profile Session is required")
	}
	targets, ports, err := normalizeTargets(request.Targets)
	if err != nil {
		return Info{}, err
	}
	task, err := manager.client.CreateExchange(ctx, serverProfile, session, remote.ExchangeSpec{
		Service: strings.TrimSpace(request.Service), Ports: ports,
	}, "exchange:"+uuid.NewString())
	if err != nil {
		return Info{}, err
	}
	if err := matchTaskTargets(task, targets); err != nil {
		_, stopErr := manager.client.StopExchange(ctx, serverProfile, session, task.ID)
		return Info{}, errors.Join(err, stopErr)
	}
	connection, err := manager.streams.OpenTrafficStream(ctx, serverProfile.ID, tunnel.TrafficModeExchange, task.ID)
	if err != nil || connection == nil {
		if err == nil {
			err = errors.New("data Plane returned an empty Exchange stream")
		}
		_, stopErr := manager.client.StopExchange(ctx, serverProfile, session, task.ID)
		return Info{}, errors.Join(err, stopErr)
	}
	relay := reverserelay.New(connection, targets, manager.dial)
	if err := relay.ReadReady(ctx); err != nil {
		_ = connection.Close()
		_, stopErr := manager.client.StopExchange(ctx, serverProfile, session, task.ID)
		return Info{}, errors.Join(err, stopErr)
	}
	runContext, cancel := context.WithCancel(context.Background())
	entry := &activeExchange{
		profile: serverProfile, session: session, task: task, relay: relay, cancel: cancel, done: make(chan struct{}),
		info: Info{
			ID:        task.ID,
			ProfileID: serverProfile.ID,
			SessionID: session.ID,
			Namespace: session.Namespace,
			Service:   task.Service,
			ClusterIP: task.ClusterIP,
			State:     "running",
			Targets:   append([]LocalTarget(nil), targets...),
		},
	}
	manager.mu.Lock()
	if _, exists := manager.active[task.ID]; exists {
		manager.mu.Unlock()
		cancel()
		_ = connection.Close()
		_, stopErr := manager.client.StopExchange(ctx, serverProfile, session, task.ID)
		return Info{}, errors.Join(errors.New("exchange Task is already active locally"), stopErr)
	}
	manager.active[task.ID] = entry
	manager.mu.Unlock()
	go manager.run(runContext, entry)
	return entry.info, nil
}

func (manager *Manager) Stop(ctx context.Context, profileID, taskID string) error {
	if ctx == nil {
		return errors.New("exchange stop context is required")
	}
	manager.mu.Lock()
	entry := manager.active[taskID]
	if entry == nil || entry.profile.ID != profileID {
		entry = nil
	} else {
		delete(manager.active, taskID)
	}
	manager.mu.Unlock()
	if entry == nil {
		return errors.New("exchange is not active locally")
	}
	// Persist the stop request before notifying the stream owner. Sending the
	// stream frame first lets a fast owner race the DELETE state transition and
	// turn an otherwise idempotent stop into a 409 conflict.
	_, remoteErr := manager.client.StopExchange(ctx, entry.profile, entry.session, entry.task.ID)
	streamErr := entry.relay.Stop(ctx)
	entry.cancel()
	select {
	case <-entry.done:
	case <-ctx.Done():
		streamErr = errors.Join(streamErr, ctx.Err())
	}
	return errors.Join(remoteErr, streamErr)
}

func (manager *Manager) List(profileID string) []Info {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	items := make([]Info, 0, len(manager.active))
	for _, entry := range manager.active {
		if profileID == "" || entry.profile.ID == profileID {
			item := entry.info
			item.Targets = append([]LocalTarget(nil), item.Targets...)
			items = append(items, item)
		}
	}
	slices.SortFunc(items, func(left, right Info) int { return strings.Compare(left.ID, right.ID) })
	return items
}

func (manager *Manager) StopProfile(ctx context.Context, profileID string) error {
	manager.mu.Lock()
	ids := make([]string, 0)
	for id, entry := range manager.active {
		if entry.profile.ID == profileID {
			ids = append(ids, id)
		}
	}
	manager.mu.Unlock()
	slices.Sort(ids)
	var result error
	for _, id := range ids {
		result = errors.Join(result, manager.Stop(ctx, profileID, id))
	}
	return result
}

func (manager *Manager) Shutdown(ctx context.Context) error {
	if ctx == nil {
		return errors.New("exchange shutdown context is required")
	}
	manager.mu.Lock()
	ids := make([]string, 0, len(manager.active))
	profiles := make(map[string]string, len(manager.active))
	for id, entry := range manager.active {
		ids = append(ids, id)
		profiles[id] = entry.profile.ID
	}
	manager.mu.Unlock()
	slices.Sort(ids)
	var result error
	for _, id := range ids {
		result = errors.Join(result, manager.Stop(ctx, profiles[id], id))
	}
	return result
}

func (manager *Manager) run(ctx context.Context, entry *activeExchange) {
	defer close(entry.done)
	_ = entry.relay.Run(ctx)
	entry.cancel()
	manager.mu.Lock()
	if manager.active[entry.task.ID] == entry {
		delete(manager.active, entry.task.ID)
	}
	manager.mu.Unlock()
}
