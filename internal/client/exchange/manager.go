package exchange

import (
	"context"
	"errors"
	"io"
	"net"
	"slices"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/client/reverserelay"
	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
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
	ErrClosed            = errors.New("exchange manager is closed")
	ErrNotManagedLocally = errors.New("exchange is not managed locally")
)

const (
	exchangeStateRunning = "running"
	exchangeStatePaused  = "paused"
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

	lifecycle sync.RWMutex
	closed    bool
	mu        sync.Mutex
	active    map[string]*activeExchange
	deleted   map[string]struct{}
}

func (manager *Manager) Start(
	ctx context.Context,
	serverProfile profile.Profile,
	session remote.Session,
	request Request,
) (Info, error) {
	manager.lifecycle.RLock()
	defer manager.lifecycle.RUnlock()
	if manager.closed {
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
	if err := matchTaskTargets(task, targets); err != nil {
		_, stopErr := deleteExchange(ctx, manager.client, serverProfile, session, task.ID)
		return Info{}, errors.Join(err, stopErr)
	}
	connection, err := manager.streams.OpenTrafficStream(ctx, serverProfile.ID, tunnel.TrafficModeExchange, task.ID)
	if err != nil || connection == nil {
		if err == nil {
			err = errors.New("data Plane returned an empty Exchange stream")
		}
		_, stopErr := deleteExchange(ctx, manager.client, serverProfile, session, task.ID)
		return Info{}, errors.Join(err, stopErr)
	}
	relay := reverserelay.New(connection, targets, manager.dial)
	if err := relay.ReadReady(ctx); err != nil {
		_ = connection.Close()
		_, stopErr := deleteExchange(ctx, manager.client, serverProfile, session, task.ID)
		return Info{}, errors.Join(err, stopErr)
	}
	runContext, cancel := context.WithCancel(context.WithoutCancel(ctx))
	entry := &activeExchange{
		profile: serverProfile, session: session, task: task, relay: relay, cancel: cancel, done: make(chan struct{}),
		info: Info{
			ID:        task.ID,
			ProfileID: serverProfile.ID,
			SessionID: session.ID,
			Namespace: session.Namespace,
			Service:   task.Service,
			ClusterIP: task.ClusterIP,
			State:     exchangeStateRunning,
			Targets:   append([]LocalTarget(nil), targets...),
		},
	}
	manager.mu.Lock()
	if _, exists := manager.active[task.ID]; exists {
		manager.mu.Unlock()
		cancel()
		_ = connection.Close()
		_, stopErr := deleteExchange(ctx, manager.client, serverProfile, session, task.ID)
		return Info{}, errors.Join(errors.New("exchange Task is already active locally"), stopErr)
	}
	manager.active[task.ID] = entry
	manager.mu.Unlock()
	go manager.run(runContext, entry)
	return entry.info, nil
}

func (manager *Manager) Pause(ctx context.Context, profileID, taskID string) error {
	if ctx == nil {
		return errors.New("exchange pause context is required")
	}
	manager.mu.Lock()
	entry := manager.active[taskID]
	if entry == nil || entry.profile.ID != profileID ||
		(entry.info.State != "" && entry.info.State != exchangeStateRunning) {
		entry = nil
	} else {
		entry.info.State = "pausing"
	}
	manager.mu.Unlock()
	if entry == nil {
		return errors.New("exchange is not active locally")
	}
	// Persist the stop request before notifying the stream owner. Sending the
	// stream frame first lets a fast owner race the DELETE state transition and
	// turn an otherwise idempotent stop into a 409 conflict.
	_, remoteErr := pauseExchange(ctx, manager.client, entry.profile, entry.session, entry.task.ID)
	streamErr := entry.relay.Stop(ctx)
	if remoteErr == nil && isClosedTrafficStream(streamErr) {
		streamErr = nil
	}
	entry.cancel()
	select {
	case <-entry.done:
	case <-ctx.Done():
		streamErr = errors.Join(streamErr, ctx.Err())
	}
	manager.mu.Lock()
	if manager.active[taskID] == entry {
		entry.info.State = exchangeStatePaused
		entry.relay, entry.cancel, entry.done = nil, nil, nil
	}
	manager.mu.Unlock()
	return errors.Join(remoteErr, streamErr)
}

func isClosedTrafficStream(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) ||
		errors.Is(err, net.ErrClosed)
}

func (manager *Manager) stopLocal(ctx context.Context, entry *activeExchange) error {
	manager.mu.Lock()
	if manager.active[entry.task.ID] != entry || entry.info.State != exchangeStateRunning {
		manager.mu.Unlock()
		return nil
	}
	entry.info.State = "pausing"
	manager.mu.Unlock()
	streamErr := entry.relay.Stop(ctx)
	if isClosedTrafficStream(streamErr) {
		streamErr = nil
	}
	entry.cancel()
	select {
	case <-entry.done:
	case <-ctx.Done():
		streamErr = errors.Join(streamErr, ctx.Err())
	}
	manager.mu.Lock()
	if manager.active[entry.task.ID] == entry {
		entry.info.State = exchangeStatePaused
		entry.relay, entry.cancel, entry.done = nil, nil, nil
	}
	manager.mu.Unlock()
	return streamErr
}

func (manager *Manager) Resume(ctx context.Context, profileID, taskID string) (Info, error) {
	if ctx == nil {
		return Info{}, errors.New("exchange resume context is required")
	}
	manager.mu.Lock()
	paused := manager.active[taskID]
	if paused == nil || paused.profile.ID != profileID || paused.info.State != "paused" {
		paused = nil
	}
	manager.mu.Unlock()
	if paused == nil {
		return Info{}, errors.New("exchange is not paused locally")
	}
	task, err := resumeExchange(ctx, manager.client, paused.profile, paused.session, paused.task.ID)
	if err != nil {
		return Info{}, err
	}
	return manager.startLocal(ctx, paused, task, true)
}

func (manager *Manager) startLocal(
	ctx context.Context,
	paused *activeExchange,
	task remote.ExchangeTask,
	compensateRemote bool,
) (Info, error) {
	connection, err := manager.streams.OpenTrafficStream(ctx, paused.profile.ID, tunnel.TrafficModeExchange, task.ID)
	if err != nil || connection == nil {
		if err == nil {
			err = errors.New("data Plane returned an empty Exchange stream")
		}
		if !compensateRemote {
			return Info{}, err
		}
		_, remoteErr := manager.client.StopExchange(ctx, paused.profile, paused.session, task.ID)
		return Info{}, errors.Join(err, remoteErr)
	}
	finiteTargets := append([]LocalTarget(nil), paused.info.Targets...)
	relay := reverserelay.New(connection, finiteTargets, manager.dial)
	if err := relay.ReadReady(ctx); err != nil {
		_ = connection.Close()
		if !compensateRemote {
			return Info{}, err
		}
		_, remoteErr := manager.client.StopExchange(ctx, paused.profile, paused.session, task.ID)
		return Info{}, errors.Join(err, remoteErr)
	}
	runContext, cancel := context.WithCancel(context.WithoutCancel(ctx))
	entry := &activeExchange{
		profile: paused.profile, session: paused.session, task: task, relay: relay,
		cancel: cancel, done: make(chan struct{}), info: paused.info,
	}
	entry.info.State = exchangeStateRunning
	manager.mu.Lock()
	manager.active[task.ID] = entry
	manager.mu.Unlock()
	go manager.run(runContext, entry)
	return entry.info, nil
}

func (manager *Manager) Delete(ctx context.Context, profileID, taskID string) error {
	if ctx == nil {
		return errors.New("exchange delete context is required")
	}
	manager.mu.Lock()
	entry := manager.active[taskID]
	manager.mu.Unlock()
	if entry == nil || entry.profile.ID != profileID {
		return ErrNotManagedLocally
	}
	var pauseErr error
	if entry.info.State == exchangeStateRunning {
		pauseErr = manager.Pause(ctx, profileID, taskID)
	}
	_, deleteErr := deleteExchange(ctx, manager.client, entry.profile, entry.session, entry.task.ID)
	if deleteErr == nil {
		manager.mu.Lock()
		delete(manager.active, taskID)
		manager.deleted[taskID] = struct{}{}
		manager.mu.Unlock()
	}
	return errors.Join(pauseErr, deleteErr)
}

func resumeExchange(
	ctx context.Context, client Client, serverProfile profile.Profile, session remote.Session, taskID string,
) (remote.ExchangeTask, error) {
	lifecycle, ok := client.(interface {
		ResumeExchange(context.Context, profile.Profile, remote.Session, string) (remote.ExchangeTask, error)
	})
	if !ok {
		return remote.ExchangeTask{}, errors.New("exchange resume is unavailable")
	}
	return lifecycle.ResumeExchange(ctx, serverProfile, session, taskID)
}

func pauseExchange(
	ctx context.Context, client Client, serverProfile profile.Profile, session remote.Session, taskID string,
) (remote.ExchangeTask, error) {
	pauser, ok := client.(interface {
		PauseExchange(context.Context, profile.Profile, remote.Session, string) (remote.ExchangeTask, error)
	})
	if !ok {
		return client.StopExchange(ctx, serverProfile, session, taskID)
	}
	return pauser.PauseExchange(ctx, serverProfile, session, taskID)
}

func deleteExchange(
	ctx context.Context, client Client, serverProfile profile.Profile, session remote.Session, taskID string,
) (remote.ExchangeTask, error) {
	lifecycle, ok := client.(interface {
		DeleteExchange(context.Context, profile.Profile, remote.Session, string) (remote.ExchangeTask, error)
	})
	if !ok {
		return client.StopExchange(ctx, serverProfile, session, taskID)
	}
	return lifecycle.DeleteExchange(ctx, serverProfile, session, taskID)
}

// Stop is retained for internal compatibility. User-facing APIs use Pause.
func (manager *Manager) Stop(ctx context.Context, profileID, taskID string) error {
	err := manager.Pause(ctx, profileID, taskID)
	if err == nil {
		manager.mu.Lock()
		delete(manager.active, taskID)
		manager.mu.Unlock()
	}
	return err
}

// StopProfile is retained for internal compatibility. User-facing APIs use PauseProfile.
func (manager *Manager) StopProfile(ctx context.Context, profileID string) error {
	if ctx == nil {
		return errors.New("exchange stop Profile context is required")
	}
	manager.lifecycle.Lock()
	defer manager.lifecycle.Unlock()
	manager.mu.Lock()
	ids := make([]string, 0)
	for id, entry := range manager.active {
		if entry.profile.ID == profileID && (entry.info.State == "" || entry.info.State == exchangeStateRunning) {
			ids = append(ids, id)
		}
	}
	manager.mu.Unlock()
	var result error
	for _, id := range ids {
		result = errors.Join(result, manager.Stop(ctx, profileID, id))
	}
	return result
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

func (manager *Manager) PauseProfile(ctx context.Context, profileID string) error {
	if ctx == nil {
		return errors.New("exchange pause Profile context is required")
	}
	manager.lifecycle.Lock()
	defer manager.lifecycle.Unlock()
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
		manager.mu.Lock()
		entry := manager.active[id]
		manager.mu.Unlock()
		if entry != nil && entry.info.State == exchangeStateRunning {
			result = errors.Join(result, manager.Pause(ctx, profileID, id))
		}
	}
	return result
}

func (manager *Manager) Shutdown(ctx context.Context) error {
	if ctx == nil {
		return errors.New("exchange shutdown context is required")
	}
	manager.lifecycle.Lock()
	defer manager.lifecycle.Unlock()
	manager.closed = true
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
		manager.mu.Lock()
		entry := manager.active[id]
		manager.mu.Unlock()
		if entry != nil && entry.info.State == exchangeStateRunning {
			result = errors.Join(result, manager.Stop(ctx, profiles[id], id))
		}
	}
	return result
}

func (manager *Manager) run(ctx context.Context, entry *activeExchange) {
	defer close(entry.done)
	_ = entry.relay.Run(ctx)
	entry.cancel()
	manager.mu.Lock()
	if manager.active[entry.task.ID] == entry && entry.info.State == exchangeStateRunning {
		delete(manager.active, entry.task.ID)
	}
	manager.mu.Unlock()
}
