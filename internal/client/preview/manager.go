package preview

import (
	"context"
	"errors"
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

var ErrClosed = errors.New("preview manager is closed")

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

type activePreview struct {
	profile profile.Profile
	session remote.Session
	task    remote.PreviewTask
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
	active    map[string]*activePreview
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
	if ctx == nil || strings.TrimSpace(request.ProfileID) != serverProfile.ID || session.State != previewSessionActive {
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
		Name: request.Name, Ports: ports,
	}, "preview:"+uuid.NewString())
	if err != nil {
		return Info{}, err
	}
	if err := matchTask(task, request.Name, targets); err != nil {
		_, stopErr := deletePreview(ctx, manager.client, serverProfile, session, task.ID)
		return Info{}, errors.Join(err, stopErr)
	}
	connection, err := manager.streams.OpenTrafficStream(ctx, serverProfile.ID, tunnel.TrafficModePreview, task.ID)
	if err != nil || connection == nil {
		if err == nil {
			err = errors.New("data Plane returned an empty Preview stream")
		}
		_, stopErr := deletePreview(ctx, manager.client, serverProfile, session, task.ID)
		return Info{}, errors.Join(err, stopErr)
	}
	relay := reverserelay.New(connection, targets, manager.dial)
	if err := relay.ReadReady(ctx); err != nil {
		_ = connection.Close()
		_, stopErr := deletePreview(ctx, manager.client, serverProfile, session, task.ID)
		return Info{}, errors.Join(err, stopErr)
	}
	running, err := manager.client.GetPreview(ctx, serverProfile, session, task.ID)
	if err != nil || running.State != previewTaskRunning || net.ParseIP(running.ClusterIP) == nil {
		if err == nil {
			err = errors.New("gateway returned an incomplete running Preview")
		}
		_, stopErr := deletePreview(ctx, manager.client, serverProfile, session, task.ID)
		streamErr := relay.Stop(ctx)
		_ = connection.Close()
		return Info{}, errors.Join(err, stopErr, streamErr)
	}
	if err := matchTask(running, request.Name, targets); err != nil {
		_, stopErr := deletePreview(ctx, manager.client, serverProfile, session, task.ID)
		streamErr := relay.Stop(ctx)
		_ = connection.Close()
		return Info{}, errors.Join(err, stopErr, streamErr)
	}

	runContext, cancel := context.WithCancel(context.WithoutCancel(ctx))
	entry := &activePreview{
		profile: serverProfile,
		session: session,
		task:    running,
		relay:   relay,
		cancel:  cancel,
		done:    make(chan struct{}),
		info: Info{
			ID:        running.ID,
			ProfileID: serverProfile.ID,
			SessionID: session.ID,
			Namespace: session.Namespace,
			Name:      running.Name,
			ClusterIP: running.ClusterIP,
			State:     previewTaskRunning,
			Targets:   append([]LocalTarget(nil), targets...),
		},
	}
	manager.mu.Lock()
	if _, exists := manager.active[running.ID]; exists {
		manager.mu.Unlock()
		cancel()
		_, stopErr := deletePreview(ctx, manager.client, serverProfile, session, running.ID)
		streamErr := relay.Stop(ctx)
		_ = connection.Close()
		return Info{}, errors.Join(errors.New("preview Task is already active locally"), stopErr, streamErr)
	}
	manager.active[running.ID] = entry
	manager.mu.Unlock()
	go manager.run(runContext, entry)
	return entry.info, nil
}

func (manager *Manager) Pause(ctx context.Context, profileID, taskID string) error {
	if ctx == nil {
		return errors.New("preview pause context is required")
	}
	manager.mu.Lock()
	entry := manager.active[taskID]
	if entry == nil || entry.profile.ID != profileID ||
		(entry.info.State != "" && entry.info.State != previewTaskRunning) {
		entry = nil
	} else {
		entry.info.State = "pausing"
	}
	manager.mu.Unlock()
	if entry == nil {
		return errors.New("preview is not active locally")
	}
	// The durable stop request wins the race with the stream owner's cleanup.
	_, remoteErr := pausePreview(ctx, manager.client, entry.profile, entry.session, entry.task.ID)
	streamErr := entry.relay.Stop(ctx)
	entry.cancel()
	select {
	case <-entry.done:
	case <-ctx.Done():
		streamErr = errors.Join(streamErr, ctx.Err())
	}
	manager.mu.Lock()
	if manager.active[taskID] == entry {
		entry.info.State = "paused"
		entry.relay, entry.cancel, entry.done = nil, nil, nil
	}
	manager.mu.Unlock()
	return errors.Join(remoteErr, streamErr)
}

func (manager *Manager) Resume(ctx context.Context, profileID, taskID string) (Info, error) {
	if ctx == nil {
		return Info{}, errors.New("preview resume context is required")
	}
	manager.mu.Lock()
	paused := manager.active[taskID]
	if paused == nil || paused.profile.ID != profileID || paused.info.State != "paused" {
		paused = nil
	}
	manager.mu.Unlock()
	if paused == nil {
		return Info{}, errors.New("preview is not paused locally")
	}
	task, err := resumePreview(ctx, manager.client, paused.profile, paused.session, paused.task.ID)
	if err != nil {
		return Info{}, err
	}
	connection, err := manager.streams.OpenTrafficStream(ctx, paused.profile.ID, tunnel.TrafficModePreview, task.ID)
	if err != nil || connection == nil {
		if err == nil {
			err = errors.New("data Plane returned an empty Preview stream")
		}
		_, pauseErr := manager.client.StopPreview(ctx, paused.profile, paused.session, task.ID)
		return Info{}, errors.Join(err, pauseErr)
	}
	targets := append([]LocalTarget(nil), paused.info.Targets...)
	relay := reverserelay.New(connection, targets, manager.dial)
	if err := relay.ReadReady(ctx); err != nil {
		_ = connection.Close()
		_, pauseErr := manager.client.StopPreview(ctx, paused.profile, paused.session, task.ID)
		return Info{}, errors.Join(err, pauseErr)
	}
	running, err := manager.client.GetPreview(ctx, paused.profile, paused.session, task.ID)
	if err != nil || running.State != previewTaskRunning || net.ParseIP(running.ClusterIP) == nil {
		if err == nil {
			err = errors.New("gateway returned an incomplete running Preview")
		}
		_, pauseErr := manager.client.StopPreview(ctx, paused.profile, paused.session, task.ID)
		_ = relay.Stop(ctx)
		return Info{}, errors.Join(err, pauseErr)
	}
	runContext, cancel := context.WithCancel(context.WithoutCancel(ctx))
	entry := &activePreview{
		profile: paused.profile, session: paused.session, task: running, relay: relay,
		cancel: cancel, done: make(chan struct{}), info: paused.info,
	}
	entry.info.State, entry.info.ClusterIP = previewTaskRunning, running.ClusterIP
	manager.mu.Lock()
	manager.active[taskID] = entry
	manager.mu.Unlock()
	go manager.run(runContext, entry)
	return entry.info, nil
}

func (manager *Manager) Delete(ctx context.Context, profileID, taskID string) error {
	if ctx == nil {
		return errors.New("preview delete context is required")
	}
	manager.mu.Lock()
	entry := manager.active[taskID]
	manager.mu.Unlock()
	if entry == nil || entry.profile.ID != profileID {
		return errors.New("preview is not managed locally")
	}
	var pauseErr error
	if entry.info.State == previewTaskRunning {
		pauseErr = manager.Pause(ctx, profileID, taskID)
	}
	_, deleteErr := deletePreview(ctx, manager.client, entry.profile, entry.session, entry.task.ID)
	if deleteErr == nil {
		manager.mu.Lock()
		delete(manager.active, taskID)
		manager.mu.Unlock()
	}
	return errors.Join(pauseErr, deleteErr)
}

func resumePreview(
	ctx context.Context, client Client, serverProfile profile.Profile, session remote.Session, taskID string,
) (remote.PreviewTask, error) {
	lifecycle, ok := client.(interface {
		ResumePreview(context.Context, profile.Profile, remote.Session, string) (remote.PreviewTask, error)
	})
	if !ok {
		return remote.PreviewTask{}, errors.New("preview resume is unavailable")
	}
	return lifecycle.ResumePreview(ctx, serverProfile, session, taskID)
}

func pausePreview(
	ctx context.Context, client Client, serverProfile profile.Profile, session remote.Session, taskID string,
) (remote.PreviewTask, error) {
	pauser, ok := client.(interface {
		PausePreview(context.Context, profile.Profile, remote.Session, string) (remote.PreviewTask, error)
	})
	if !ok {
		return client.StopPreview(ctx, serverProfile, session, taskID)
	}
	return pauser.PausePreview(ctx, serverProfile, session, taskID)
}

func deletePreview(
	ctx context.Context, client Client, serverProfile profile.Profile, session remote.Session, taskID string,
) (remote.PreviewTask, error) {
	lifecycle, ok := client.(interface {
		DeletePreview(context.Context, profile.Profile, remote.Session, string) (remote.PreviewTask, error)
	})
	if !ok {
		return client.StopPreview(ctx, serverProfile, session, taskID)
	}
	return lifecycle.DeletePreview(ctx, serverProfile, session, taskID)
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
		return errors.New("preview stop Profile context is required")
	}
	manager.lifecycle.Lock()
	defer manager.lifecycle.Unlock()
	manager.mu.Lock()
	ids := make([]string, 0)
	for id, entry := range manager.active {
		if entry.profile.ID == profileID && (entry.info.State == "" || entry.info.State == previewTaskRunning) {
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
		return errors.New("preview pause Profile context is required")
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
		if entry != nil && entry.info.State == previewTaskRunning {
			result = errors.Join(result, manager.Pause(ctx, profileID, id))
		}
	}
	return result
}

func (manager *Manager) Shutdown(ctx context.Context) error {
	if ctx == nil {
		return errors.New("preview shutdown context is required")
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
		if entry != nil && entry.info.State == previewTaskRunning {
			result = errors.Join(result, manager.Stop(ctx, profiles[id], id))
		}
	}
	return result
}

func (manager *Manager) run(ctx context.Context, entry *activePreview) {
	defer close(entry.done)
	_ = entry.relay.Run(ctx)
	entry.cancel()
	manager.mu.Lock()
	if manager.active[entry.task.ID] == entry && entry.info.State == previewTaskRunning {
		delete(manager.active, entry.task.ID)
	}
	manager.mu.Unlock()
}
