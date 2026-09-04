package taskrelay

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
)

type active struct {
	profileID string
	profile   profile.Profile
	session   remote.Session
	task      Task
	relay     Relay
	cancel    context.CancelFunc
	done      chan struct{}
	state     string
}

func (entry *active) snapshot() Entry {
	return Entry{
		ProfileID: entry.profileID,
		Task: Task{
			ID: entry.task.ID, SessionID: entry.task.SessionID,
			Namespace: entry.task.Namespace, Service: entry.task.Service,
			ClusterIP: entry.task.ClusterIP, Running: entry.task.Running,
			Targets: append([]Target(nil), entry.task.Targets...),
		},
		State: entry.state,
	}
}

// Manager tracks the locally relayed tasks of one task type. D is the wire
// document the owning manager hands the desktop.
type Manager[D any] struct {
	// name is the task type in user-facing errors: "exchange", "mirror",
	// "preview".
	name     string
	gateway  Gateway
	open     Open
	describe func(Entry) D
	confirm  Confirm

	lifecycle sync.RWMutex
	closed    bool
	mu        sync.Mutex
	entries   map[string]*active
}

// Config describes one task type to the shared manager.
type Config[D any] struct {
	// Name is the task type in user-facing errors: "exchange", "mirror",
	// "preview".
	Name    string
	Gateway Gateway
	Open    Open
	// Describe renders one entry as the caller's own wire document.
	Describe func(Entry) D
	// Confirm optionally validates and refines a task once its relay is ready
	// but before the manager adopts it, so a task whose readiness only becomes
	// observable then -- a Preview's ClusterIP, say -- can be checked. stored
	// is what the manager already shows, current the Gateway's latest view.
	// Returning an error tears the relay back down.
	Confirm Confirm
}

// Reason says why the manager is starting a relay for a task it already holds.
type Reason int

const (
	// Resumed follows a client-requested resume. The Gateway state the manager
	// holds is stale, and a local failure is compensated by pausing the task
	// again so it is never left running with no relay.
	Resumed Reason = iota
	// Restored follows reconciliation against Gateway state the manager has
	// just read, so a local failure is reported without touching the Gateway.
	Restored
)

// Confirm validates and refines a task once its relay is ready.
type Confirm func(
	ctx context.Context,
	serverProfile profile.Profile,
	session remote.Session,
	stored, current Task,
	reason Reason,
) (Task, error)

func New[D any](config Config[D]) (*Manager[D], error) {
	if strings.TrimSpace(config.Name) == "" || config.Gateway == nil ||
		config.Open == nil || config.Describe == nil {
		return nil, errors.New(
			"task relay name, Gateway, stream opener and document renderer are required",
		)
	}
	return &Manager[D]{
		name: config.Name, gateway: config.Gateway, open: config.Open,
		describe: config.Describe, confirm: config.Confirm,
		entries: make(map[string]*active),
	}, nil
}

func (manager *Manager[D]) errorf(format string) error {
	return errors.New(manager.name + " " + format)
}

// Adopt takes ownership of a task whose relay is already open and running-ready,
// starts carrying its traffic and returns the document for it. The caller has
// created the task against the Gateway; everything after that is shared.
func (manager *Manager[D]) Adopt(
	ctx context.Context,
	serverProfile profile.Profile,
	session remote.Session,
	task Task,
	relay Relay,
) (D, error) {
	var zero D
	runContext, cancel := context.WithCancel(context.WithoutCancel(ctx))
	entry := &active{
		profileID: serverProfile.ID, profile: serverProfile, session: session,
		task: task, relay: relay, cancel: cancel, done: make(chan struct{}),
		state: StateRunning,
	}
	manager.mu.Lock()
	if _, exists := manager.entries[task.ID]; exists {
		manager.mu.Unlock()
		cancel()
		return zero, manager.errorf("Task is already active locally")
	}
	manager.entries[task.ID] = entry
	document := manager.describe(entry.snapshot())
	manager.mu.Unlock()
	go manager.run(runContext, entry)
	return document, nil
}

// Closed reports whether Shutdown has run. Start must refuse afterwards.
func (manager *Manager[D]) Closed() bool {
	manager.lifecycle.RLock()
	defer manager.lifecycle.RUnlock()
	return manager.closed
}

// Hold blocks Shutdown for the duration of a Start. The caller must call the
// returned release exactly once.
func (manager *Manager[D]) Hold() (release func(), closed bool) {
	manager.lifecycle.RLock()
	if manager.closed {
		manager.lifecycle.RUnlock()
		return func() {}, true
	}
	return manager.lifecycle.RUnlock, false
}

// Pause releases the task's Gateway resources and tears down the local relay,
// leaving the durable task in place so Resume can bring it back.
func (manager *Manager[D]) Pause(ctx context.Context, profileID, taskID string) error {
	if ctx == nil {
		return manager.errorf("pause context is required")
	}
	manager.mu.Lock()
	entry := manager.entries[taskID]
	if entry == nil || entry.profileID != profileID ||
		(entry.state != "" && entry.state != StateRunning) {
		entry = nil
	} else {
		entry.state = StatePausing
	}
	manager.mu.Unlock()
	if entry == nil {
		return manager.errorf("is not active locally")
	}
	// Persist the stop request before notifying the stream owner. Sending the
	// stream frame first lets a fast owner race the DELETE state transition and
	// turn an otherwise idempotent stop into a 409 conflict.
	remoteErr := manager.gateway.Pause(ctx, entry.profile, entry.session, entry.task.ID)
	streamErr := manager.release(ctx, entry)
	if remoteErr == nil && ClosedStream(streamErr) {
		streamErr = nil
	}
	return errors.Join(remoteErr, streamErr)
}

// release stops the local relay and waits for its goroutine, then records the
// entry as paused. It never touches the Gateway.
func (manager *Manager[D]) release(ctx context.Context, entry *active) error {
	streamErr := entry.relay.Stop(ctx)
	entry.cancel()
	select {
	case <-entry.done:
	case <-ctx.Done():
		streamErr = errors.Join(streamErr, ctx.Err())
	}
	manager.mu.Lock()
	if manager.entries[entry.task.ID] == entry {
		entry.state = StatePaused
		entry.relay, entry.cancel, entry.done = nil, nil, nil
	}
	manager.mu.Unlock()
	return streamErr
}

// stopLocal tears down the local relay of a running entry without asking the
// Gateway to pause the task.
func (manager *Manager[D]) stopLocal(ctx context.Context, entry *active) error {
	manager.mu.Lock()
	if manager.entries[entry.task.ID] != entry || entry.state != StateRunning {
		manager.mu.Unlock()
		return nil
	}
	entry.state = StatePausing
	manager.mu.Unlock()
	streamErr := manager.release(ctx, entry)
	if ClosedStream(streamErr) {
		streamErr = nil
	}
	return streamErr
}

func (manager *Manager[D]) Resume(ctx context.Context, profileID, taskID string) (D, error) {
	var zero D
	if ctx == nil {
		return zero, manager.errorf("resume context is required")
	}
	manager.mu.Lock()
	paused := manager.entries[taskID]
	if paused == nil || paused.profileID != profileID || paused.state != StatePaused {
		paused = nil
	}
	manager.mu.Unlock()
	if paused == nil {
		return zero, manager.errorf("is not paused locally")
	}
	current, err := manager.gateway.Resume(ctx, paused.profile, paused.session, paused.task.ID)
	if err != nil {
		return zero, err
	}
	return manager.startLocal(ctx, paused, current, Resumed)
}

// startLocal re-opens the relay of a paused entry. compensateGateway asks the
// Gateway to pause the task again when the local side cannot be brought up,
// so a failed resume does not leave a task running with no relay.
func (manager *Manager[D]) startLocal(
	ctx context.Context,
	paused *active,
	current Task,
	reason Reason,
) (D, error) {
	var zero D
	compensate := reason == Resumed
	fail := func(err error) (D, error) {
		if !compensate {
			return zero, err
		}
		return zero, errors.Join(err, manager.gateway.Pause(
			ctx, paused.profile, paused.session, paused.task.ID,
		))
	}
	// Keep the description this manager recorded when it first observed the
	// task. The Gateway echoes neither the local targets nor, on resume, a
	// fresh description, so re-reading them here would blank the document.
	task := paused.task
	task.Running = true
	relay, closeStream, err := manager.open(ctx, paused.profile, task)
	if err != nil {
		return fail(err)
	}
	if err := relay.ReadReady(ctx); err != nil {
		_ = closeStream()
		return fail(err)
	}
	if manager.confirm != nil {
		confirmed, confirmErr := manager.confirm(
			ctx, paused.profile, paused.session, task, current, reason,
		)
		if confirmErr != nil {
			_ = relay.Stop(ctx)
			_ = closeStream()
			return fail(confirmErr)
		}
		task = confirmed
	}
	runContext, cancel := context.WithCancel(context.WithoutCancel(ctx))
	entry := &active{
		profileID: paused.profileID, profile: paused.profile, session: paused.session,
		task: task, relay: relay, cancel: cancel, done: make(chan struct{}),
		state: StateRunning,
	}
	manager.mu.Lock()
	manager.entries[task.ID] = entry
	document := manager.describe(entry.snapshot())
	manager.mu.Unlock()
	go manager.run(runContext, entry)
	return document, nil
}

func (manager *Manager[D]) Delete(ctx context.Context, profileID, taskID string) error {
	if ctx == nil {
		return manager.errorf("delete context is required")
	}
	manager.mu.Lock()
	entry := manager.entries[taskID]
	manager.mu.Unlock()
	if entry == nil || entry.profileID != profileID {
		return ErrNotManagedLocally
	}
	var pauseErr error
	if entry.state == StateRunning {
		pauseErr = manager.Pause(ctx, profileID, taskID)
	}
	deleteErr := manager.gateway.Delete(ctx, entry.profile, entry.session, entry.task.ID)
	if deleteErr == nil {
		manager.mu.Lock()
		delete(manager.entries, taskID)
		manager.mu.Unlock()
	}
	return errors.Join(pauseErr, deleteErr)
}

// Stop is retained for internal compatibility. User-facing APIs use Pause.
func (manager *Manager[D]) Stop(ctx context.Context, profileID, taskID string) error {
	err := manager.Pause(ctx, profileID, taskID)
	if err == nil {
		manager.mu.Lock()
		delete(manager.entries, taskID)
		manager.mu.Unlock()
	}
	return err
}

// StopProfile is retained for internal compatibility. User-facing APIs use
// PauseProfile.
func (manager *Manager[D]) StopProfile(ctx context.Context, profileID string) error {
	if ctx == nil {
		return manager.errorf("stop Profile context is required")
	}
	manager.lifecycle.Lock()
	defer manager.lifecycle.Unlock()
	var result error
	for _, id := range manager.runningIDs(profileID) {
		result = errors.Join(result, manager.Stop(ctx, profileID, id))
	}
	return result
}

func (manager *Manager[D]) PauseProfile(ctx context.Context, profileID string) error {
	if ctx == nil {
		return manager.errorf("pause Profile context is required")
	}
	manager.lifecycle.Lock()
	defer manager.lifecycle.Unlock()
	var result error
	for _, id := range manager.runningIDs(profileID) {
		manager.mu.Lock()
		entry := manager.entries[id]
		manager.mu.Unlock()
		if entry != nil && entry.state == StateRunning {
			result = errors.Join(result, manager.Pause(ctx, profileID, id))
		}
	}
	return result
}

// ReleaseProfile stops the local relays of a profile without pausing the
// underlying Gateway tasks. Running TrafficBindings stay Running so the next
// Restore re-materializes them; released entries read as paused locally.
func (manager *Manager[D]) ReleaseProfile(ctx context.Context, profileID string) error {
	if ctx == nil {
		return manager.errorf("release Profile context is required")
	}
	manager.lifecycle.Lock()
	defer manager.lifecycle.Unlock()
	manager.mu.Lock()
	entries := make([]*active, 0, len(manager.entries))
	for _, entry := range manager.entries {
		if entry.profileID == profileID && (entry.state == "" || entry.state == StateRunning) {
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

func (manager *Manager[D]) Shutdown(ctx context.Context) error {
	if ctx == nil {
		return manager.errorf("shutdown context is required")
	}
	manager.lifecycle.Lock()
	defer manager.lifecycle.Unlock()
	manager.closed = true
	manager.mu.Lock()
	ids := make([]string, 0, len(manager.entries))
	profiles := make(map[string]string, len(manager.entries))
	for id, entry := range manager.entries {
		ids = append(ids, id)
		profiles[id] = entry.profileID
	}
	manager.mu.Unlock()
	slices.Sort(ids)
	var result error
	for _, id := range ids {
		manager.mu.Lock()
		entry := manager.entries[id]
		manager.mu.Unlock()
		if entry != nil && entry.state == StateRunning {
			result = errors.Join(result, manager.Stop(ctx, profiles[id], id))
		}
	}
	return result
}

// runningIDs returns the sorted IDs of one profile's running tasks. Callers
// hold the lifecycle lock; the returned entries may change afterwards, so each
// caller re-reads under mu before acting.
func (manager *Manager[D]) runningIDs(profileID string) []string {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	ids := make([]string, 0, len(manager.entries))
	for id, entry := range manager.entries {
		if entry.profileID == profileID && (entry.state == "" || entry.state == StateRunning) {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}

// List renders every task of one profile, or of every profile when profileID
// is empty, ordered by task ID.
func (manager *Manager[D]) List(profileID string) []D {
	manager.mu.Lock()
	snapshots := make([]Entry, 0, len(manager.entries))
	for _, entry := range manager.entries {
		if profileID == "" || entry.profileID == profileID {
			snapshots = append(snapshots, entry.snapshot())
		}
	}
	manager.mu.Unlock()
	slices.SortFunc(snapshots, func(left, right Entry) int {
		return strings.Compare(left.Task.ID, right.Task.ID)
	})
	items := make([]D, 0, len(snapshots))
	for _, snapshot := range snapshots {
		items = append(items, manager.describe(snapshot))
	}
	return items
}

// Track records a task the manager holds but does not relay, leaving it
// paused. Restore uses it for a task the Gateway still holds while the desktop
// has no relay for it; a caller reconstructing state outside a Restore needs
// the same entry point. Tracking a task the manager already holds replaces it.
func (manager *Manager[D]) Track(
	serverProfile profile.Profile, session remote.Session, task Task,
) D {
	entry := manager.track(serverProfile, session, task)
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.describe(entry.snapshot())
}

func (manager *Manager[D]) track(
	serverProfile profile.Profile, session remote.Session, task Task,
) *active {
	entry := &active{
		profileID: serverProfile.ID, profile: serverProfile, session: session,
		task: task, state: StatePaused,
	}
	manager.mu.Lock()
	manager.entries[task.ID] = entry
	manager.mu.Unlock()
	return entry
}

func (manager *Manager[D]) run(ctx context.Context, entry *active) {
	defer close(entry.done)
	_ = entry.relay.Run(ctx)
	entry.cancel()
	manager.mu.Lock()
	if manager.entries[entry.task.ID] == entry && entry.state == StateRunning {
		delete(manager.entries, entry.task.ID)
	}
	manager.mu.Unlock()
}

// Restore reconciles the local relays toward the Gateway's TrafficBinding
// state without writing remote desired state. Entries left over from an older
// Session of the same profile are dropped; a task the Gateway runs but this
// manager does not is adopted, and one it no longer runs is released.
func (manager *Manager[D]) Restore(
	ctx context.Context, serverProfile profile.Profile, session remote.Session,
) error {
	tasks, err := manager.gateway.List(ctx, serverProfile, session)
	if err != nil {
		return err
	}
	manager.dropStaleSessions(serverProfile.ID, session.ID)
	for _, task := range tasks {
		if err := manager.reconcile(ctx, serverProfile, session, task); err != nil {
			return err
		}
	}
	return nil
}

func (manager *Manager[D]) dropStaleSessions(profileID, sessionID string) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	for id, entry := range manager.entries {
		if entry.profileID == profileID && entry.session.ID != sessionID {
			if entry.cancel != nil {
				entry.cancel()
			}
			delete(manager.entries, id)
		}
	}
}

func (manager *Manager[D]) reconcile(
	ctx context.Context,
	serverProfile profile.Profile,
	session remote.Session,
	task Task,
) error {
	manager.mu.Lock()
	entry := manager.entries[task.ID]
	if entry != nil {
		// Only the Gateway's view of whether the task runs is new. The
		// description stays as first observed, matching what the desktop has
		// already been shown.
		entry.profile, entry.session = serverProfile, session
		entry.task.Running = task.Running
		state := entry.state
		manager.mu.Unlock()
		switch {
		case task.Running && state == StatePaused:
			_, err := manager.startLocal(ctx, entry, task, Restored)
			return err
		case !task.Running && state == StateRunning:
			return manager.stopLocal(ctx, entry)
		}
		return nil
	}
	manager.mu.Unlock()
	entry = manager.track(serverProfile, session, task)
	if !task.Running {
		return nil
	}
	if _, err := manager.startLocal(ctx, entry, task, Restored); err != nil {
		manager.mu.Lock()
		if manager.entries[task.ID] == entry {
			delete(manager.entries, task.ID)
		}
		manager.mu.Unlock()
		return err
	}
	return nil
}

// ErrNotManagedLocally reports a task this manager never adopted.
var ErrNotManagedLocally = errors.New("task is not managed locally")
