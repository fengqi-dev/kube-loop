package mirror

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
	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
	"github.com/fengqi-dev/kube-loop/internal/transport/trafficstream"
)

type Client interface {
	CreateMirror(context.Context, profile.Profile, remote.Session, remote.MirrorSpec, string) (remote.MirrorTask, error)
	StopMirror(context.Context, profile.Profile, remote.Session, string) (remote.MirrorTask, error)
}

type TrafficStreamOpener interface {
	OpenTrafficStream(context.Context, string, string, string) (*trafficstream.FrameConn, error)
}

var (
	ErrClosed            = errors.New("mirror manager is closed")
	ErrNotManagedLocally = errors.New("mirror is not managed locally")
)

const (
	mirrorStateRunning = "running"
	mirrorStatePaused  = "paused"
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

type activeMirror struct {
	profile profile.Profile
	session remote.Session
	task    remote.MirrorTask
	relay   *localRelay
	cancel  context.CancelFunc
	done    chan struct{}
	info    Info
}

type Manager struct {
	client  Client
	streams TrafficStreamOpener
	dial    DialContextFunc
	config  Config

	lifecycle sync.RWMutex
	closed    bool
	mu        sync.Mutex
	active    map[string]*activeMirror
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
	if ctx == nil || strings.TrimSpace(request.ProfileID) != serverProfile.ID || session.State != mirrorSessionActive {
		return Info{}, errors.New("active Server Profile Session is required")
	}
	targets, ports, err := normalizeTargets(request.Targets)
	if err != nil {
		return Info{}, err
	}
	task, err := manager.client.CreateMirror(ctx, serverProfile, session, remote.MirrorSpec{
		Service: strings.TrimSpace(request.Service), Ports: ports, LocalTargets: remoteTargets(targets),
	}, "mirror:"+uuid.NewString())
	if err != nil {
		return Info{}, err
	}
	if err := matchTaskTargets(task, targets); err != nil {
		_, stopErr := deleteMirror(ctx, manager.client, serverProfile, session, task.ID)
		return Info{}, errors.Join(err, stopErr)
	}
	connection, err := manager.streams.OpenTrafficStream(ctx, serverProfile.ID, tunnel.TrafficModeMirror, task.ID)
	if err != nil || connection == nil {
		if err == nil {
			err = errors.New("data Plane returned an empty Mirror stream")
		}
		_, stopErr := deleteMirror(ctx, manager.client, serverProfile, session, task.ID)
		return Info{}, errors.Join(err, stopErr)
	}
	relay := newLocalRelay(connection, targets, manager.dial, manager.config)
	if err := relay.readReady(ctx); err != nil {
		_ = connection.Close()
		_, stopErr := deleteMirror(ctx, manager.client, serverProfile, session, task.ID)
		return Info{}, errors.Join(err, stopErr)
	}
	runContext, cancel := context.WithCancel(context.WithoutCancel(ctx))
	entry := &activeMirror{
		profile: serverProfile, session: session, task: task, relay: relay, cancel: cancel, done: make(chan struct{}),
		info: Info{
			ID:        task.ID,
			ProfileID: serverProfile.ID,
			SessionID: session.ID,
			Namespace: session.Namespace,
			Service:   task.Service,
			ClusterIP: task.ClusterIP,
			State:     mirrorStateRunning,
			Targets:   append([]LocalTarget(nil), targets...),
		},
	}
	manager.mu.Lock()
	if _, exists := manager.active[task.ID]; exists {
		manager.mu.Unlock()
		cancel()
		_ = connection.Close()
		_, stopErr := deleteMirror(ctx, manager.client, serverProfile, session, task.ID)
		return Info{}, errors.Join(errors.New("mirror Task is already active locally"), stopErr)
	}
	manager.active[task.ID] = entry
	manager.mu.Unlock()
	go manager.run(runContext, entry)
	return entry.info, nil
}

func (manager *Manager) Pause(ctx context.Context, profileID, taskID string) error {
	if ctx == nil {
		return errors.New("mirror pause context is required")
	}
	manager.mu.Lock()
	entry := manager.active[taskID]
	if entry == nil || entry.profile.ID != profileID ||
		(entry.info.State != "" && entry.info.State != mirrorStateRunning) {
		entry = nil
	} else {
		entry.info.State = "pausing"
	}
	manager.mu.Unlock()
	if entry == nil {
		return errors.New("mirror is not active locally")
	}
	// Persist the stop request before notifying the stream owner. Sending the
	// stream frame first lets a fast owner race the DELETE state transition and
	// turn an otherwise idempotent stop into a 409 conflict.
	_, remoteErr := pauseMirror(ctx, manager.client, entry.profile, entry.session, entry.task.ID)
	streamErr := entry.relay.stop(ctx)
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
		entry.info.State = mirrorStatePaused
		entry.relay, entry.cancel, entry.done = nil, nil, nil
	}
	manager.mu.Unlock()
	return errors.Join(remoteErr, streamErr)
}

func isClosedTrafficStream(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) ||
		errors.Is(err, net.ErrClosed)
}

func (manager *Manager) stopLocal(ctx context.Context, entry *activeMirror) error {
	manager.mu.Lock()
	if manager.active[entry.task.ID] != entry || entry.info.State != mirrorStateRunning {
		manager.mu.Unlock()
		return nil
	}
	entry.info.State = "pausing"
	manager.mu.Unlock()
	streamErr := entry.relay.stop(ctx)
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
		entry.info.State = mirrorStatePaused
		entry.relay, entry.cancel, entry.done = nil, nil, nil
	}
	manager.mu.Unlock()
	return streamErr
}

func (manager *Manager) Resume(ctx context.Context, profileID, taskID string) (Info, error) {
	if ctx == nil {
		return Info{}, errors.New("mirror resume context is required")
	}
	manager.mu.Lock()
	paused := manager.active[taskID]
	if paused == nil || paused.profile.ID != profileID || paused.info.State != "paused" {
		paused = nil
	}
	manager.mu.Unlock()
	if paused == nil {
		return Info{}, errors.New("mirror is not paused locally")
	}
	task, err := resumeMirror(ctx, manager.client, paused.profile, paused.session, paused.task.ID)
	if err != nil {
		return Info{}, err
	}
	return manager.startLocal(ctx, paused, task, true)
}

func (manager *Manager) startLocal(
	ctx context.Context,
	paused *activeMirror,
	task remote.MirrorTask,
	compensateRemote bool,
) (Info, error) {
	connection, err := manager.streams.OpenTrafficStream(ctx, paused.profile.ID, tunnel.TrafficModeMirror, task.ID)
	if err != nil || connection == nil {
		if err == nil {
			err = errors.New("data Plane returned an empty Mirror stream")
		}
		if !compensateRemote {
			return Info{}, err
		}
		_, remoteErr := manager.client.StopMirror(ctx, paused.profile, paused.session, task.ID)
		return Info{}, errors.Join(err, remoteErr)
	}
	targets := append([]LocalTarget(nil), paused.info.Targets...)
	relay := newLocalRelay(connection, targets, manager.dial, manager.config)
	if err := relay.readReady(ctx); err != nil {
		_ = connection.Close()
		if !compensateRemote {
			return Info{}, err
		}
		_, remoteErr := manager.client.StopMirror(ctx, paused.profile, paused.session, task.ID)
		return Info{}, errors.Join(err, remoteErr)
	}
	runContext, cancel := context.WithCancel(context.WithoutCancel(ctx))
	entry := &activeMirror{
		profile: paused.profile, session: paused.session, task: task, relay: relay,
		cancel: cancel, done: make(chan struct{}), info: paused.info,
	}
	entry.info.State = mirrorStateRunning
	manager.mu.Lock()
	manager.active[task.ID] = entry
	manager.mu.Unlock()
	go manager.run(runContext, entry)
	return entry.info, nil
}

func (manager *Manager) Delete(ctx context.Context, profileID, taskID string) error {
	if ctx == nil {
		return errors.New("mirror delete context is required")
	}
	manager.mu.Lock()
	entry := manager.active[taskID]
	manager.mu.Unlock()
	if entry == nil || entry.profile.ID != profileID {
		return ErrNotManagedLocally
	}
	var pauseErr error
	if entry.info.State == mirrorStateRunning {
		pauseErr = manager.Pause(ctx, profileID, taskID)
	}
	_, deleteErr := deleteMirror(ctx, manager.client, entry.profile, entry.session, entry.task.ID)
	if deleteErr == nil {
		manager.mu.Lock()
		delete(manager.active, taskID)
		manager.deleted[taskID] = struct{}{}
		manager.mu.Unlock()
	}
	return errors.Join(pauseErr, deleteErr)
}

func resumeMirror(
	ctx context.Context, client Client, serverProfile profile.Profile, session remote.Session, taskID string,
) (remote.MirrorTask, error) {
	lifecycle, ok := client.(interface {
		ResumeMirror(context.Context, profile.Profile, remote.Session, string) (remote.MirrorTask, error)
	})
	if !ok {
		return remote.MirrorTask{}, errors.New("mirror resume is unavailable")
	}
	return lifecycle.ResumeMirror(ctx, serverProfile, session, taskID)
}

func pauseMirror(
	ctx context.Context, client Client, serverProfile profile.Profile, session remote.Session, taskID string,
) (remote.MirrorTask, error) {
	pauser, ok := client.(interface {
		PauseMirror(context.Context, profile.Profile, remote.Session, string) (remote.MirrorTask, error)
	})
	if !ok {
		return client.StopMirror(ctx, serverProfile, session, taskID)
	}
	return pauser.PauseMirror(ctx, serverProfile, session, taskID)
}

func deleteMirror(
	ctx context.Context, client Client, serverProfile profile.Profile, session remote.Session, taskID string,
) (remote.MirrorTask, error) {
	lifecycle, ok := client.(interface {
		DeleteMirror(context.Context, profile.Profile, remote.Session, string) (remote.MirrorTask, error)
	})
	if !ok {
		return client.StopMirror(ctx, serverProfile, session, taskID)
	}
	return lifecycle.DeleteMirror(ctx, serverProfile, session, taskID)
}

func (manager *Manager) Stop(ctx context.Context, profileID, taskID string) error {
	err := manager.Pause(ctx, profileID, taskID)
	if err == nil {
		manager.mu.Lock()
		delete(manager.active, taskID)
		manager.mu.Unlock()
	}
	return err
}

func (manager *Manager) StopProfile(ctx context.Context, profileID string) error {
	if ctx == nil {
		return errors.New("mirror stop Profile context is required")
	}
	manager.lifecycle.Lock()
	defer manager.lifecycle.Unlock()
	manager.mu.Lock()
	ids := make([]string, 0)
	for id, entry := range manager.active {
		if entry.profile.ID == profileID && (entry.info.State == "" || entry.info.State == mirrorStateRunning) {
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
		return errors.New("mirror pause Profile context is required")
	}
	manager.lifecycle.Lock()
	defer manager.lifecycle.Unlock()
	manager.mu.Lock()
	ids := make([]string, 0)
	for id, entry := range manager.active {
		if entry.profile.ID == profileID && (entry.info.State == "" || entry.info.State == mirrorStateRunning) {
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
		if entry != nil && entry.info.State == mirrorStateRunning {
			result = errors.Join(result, manager.Pause(ctx, profileID, id))
		}
	}
	return result
}

// ReleaseProfile stops the local relays of a profile without pausing the
// underlying gateway tasks. Running TrafficBindings stay Running so the next
// Restore re-materializes them; released entries read as paused locally.
func (manager *Manager) ReleaseProfile(ctx context.Context, profileID string) error {
	if ctx == nil {
		return errors.New("mirror release Profile context is required")
	}
	manager.lifecycle.Lock()
	defer manager.lifecycle.Unlock()
	manager.mu.Lock()
	entries := make([]*activeMirror, 0, len(manager.active))
	for _, entry := range manager.active {
		if entry.profile.ID == profileID && (entry.info.State == "" || entry.info.State == mirrorStateRunning) {
			entries = append(entries, entry)
		}
	}
	manager.mu.Unlock()
	var result error
	for _, entry := range entries {
		result = errors.Join(result, manager.stopLocal(ctx, entry))
	}
	return result
}

func (manager *Manager) Shutdown(ctx context.Context) error {
	if ctx == nil {
		return errors.New("mirror shutdown context is required")
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
		if entry != nil && entry.info.State == mirrorStateRunning {
			result = errors.Join(result, manager.Stop(ctx, profiles[id], id))
		}
	}
	return result
}

func (manager *Manager) run(ctx context.Context, entry *activeMirror) {
	defer close(entry.done)
	_ = entry.relay.run(ctx)
	entry.cancel()
	manager.mu.Lock()
	if manager.active[entry.task.ID] == entry && entry.info.State == mirrorStateRunning {
		delete(manager.active, entry.task.ID)
	}
	manager.mu.Unlock()
}
