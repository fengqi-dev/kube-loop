package remotesession

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
)

const (
	DefaultHeartbeatInterval = 30 * time.Second
	sessionUpdateBuffer      = 32
)

type Gateway interface {
	CreateSession(context.Context, profile.Profile, string, string) (remote.Session, error)
	HeartbeatSession(context.Context, profile.Profile, remote.Session) (remote.Session, error)
	DisconnectSession(context.Context, profile.Profile, remote.Session) (remote.Session, error)
	IssueRelayTicket(context.Context, profile.Profile, remote.Session) (remote.RelayTicket, error)
}

type Config struct {
	HeartbeatInterval time.Duration
}

type Manager struct {
	gateway     Gateway
	interval    time.Duration
	ctx         context.Context
	cancel      context.CancelFunc
	mu          sync.Mutex
	active      map[string]entry
	pendingKeys map[string]string
	updates     chan remote.SessionUpdate
	done        chan struct{}
}

type entry struct {
	profile   profile.Profile
	session   remote.Session
	lastError error
}

func New(gateway Gateway, config Config) (*Manager, error) {
	if gateway == nil {
		return nil, errors.New("remote Session Gateway is required")
	}
	if config.HeartbeatInterval <= 0 {
		config.HeartbeatInterval = DefaultHeartbeatInterval
	}
	if config.HeartbeatInterval < 10*time.Millisecond {
		return nil, errors.New("remote Session heartbeat interval is too short")
	}
	ctx, cancel := context.WithCancel(context.Background())
	manager := &Manager{
		gateway: gateway, interval: config.HeartbeatInterval, ctx: ctx, cancel: cancel,
		active: make(map[string]entry), pendingKeys: make(map[string]string),
		updates: make(chan remote.SessionUpdate, sessionUpdateBuffer), done: make(chan struct{}),
	}
	go manager.heartbeatLoop()
	return manager, nil
}

func (manager *Manager) Connect(
	ctx context.Context,
	serverProfile profile.Profile,
	namespace string,
) (remote.Session, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if current, ok := manager.active[serverProfile.ID]; ok {
		if current.session.Namespace == namespace && current.session.State == remoteSessionActive {
			if current.lastError == nil {
				return current.session, nil
			}
			if !isGone(current.lastError) {
				return current.session, current.lastError
			}
			delete(manager.active, serverProfile.ID)
		} else {
			if _, err := manager.gateway.DisconnectSession(
				ctx,
				current.profile,
				current.session,
			); err != nil &&
				!isGone(err) {
				return remote.Session{}, err
			}
			delete(manager.active, serverProfile.ID)
		}
	}
	pendingID := serverProfile.ID + "\x00" + namespace
	idempotencyKey := manager.pendingKeys[pendingID]
	if idempotencyKey == "" {
		idempotencyKey = "desktop-" + uuid.NewString()
		manager.pendingKeys[pendingID] = idempotencyKey
	}
	session, err := manager.gateway.CreateSession(ctx, serverProfile, namespace, idempotencyKey)
	if err != nil {
		return remote.Session{}, err
	}
	delete(manager.pendingKeys, pendingID)
	manager.active[serverProfile.ID] = entry{profile: serverProfile, session: session}
	return session, nil
}

func (manager *Manager) Disconnect(ctx context.Context, profileID string) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	current, ok := manager.active[profileID]
	if !ok {
		return nil
	}
	_, err := manager.gateway.DisconnectSession(ctx, current.profile, current.session)
	if err != nil && !isGone(err) {
		return err
	}
	delete(manager.active, profileID)
	return nil
}

func (manager *Manager) Current(profileID string) (remote.Session, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	current, ok := manager.active[profileID]
	if !ok {
		return remote.Session{}, errors.New("remote Session is not connected")
	}
	if current.lastError != nil {
		return current.session, current.lastError
	}
	return current.session, nil
}

func (manager *Manager) SessionUpdates() <-chan remote.SessionUpdate {
	return manager.updates
}

// Refresh performs an immediate heartbeat for Data Plane recovery. It returns
// the authoritative generation and prevents a stale reconnect from replacing a
// newer Session selected by the desktop.
func (manager *Manager) Refresh(ctx context.Context, profileID string) (remote.Session, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	current, ok := manager.active[profileID]
	if !ok {
		return remote.Session{}, errors.New("remote Session is not connected")
	}
	next, err := manager.gateway.HeartbeatSession(ctx, current.profile, current.session)
	if err != nil {
		current.lastError = err
		manager.active[profileID] = current
		return current.session, err
	}
	current.session = next
	current.lastError = nil
	manager.active[profileID] = current
	manager.publishSessionUpdateLocked(profileID, next)
	return next, nil
}

func (manager *Manager) IssueRelayTicket(
	ctx context.Context,
	profileID string,
) (remote.RelayTicket, error) {
	manager.mu.Lock()
	current, ok := manager.active[profileID]
	manager.mu.Unlock()
	if !ok {
		return remote.RelayTicket{}, errors.New("remote Session is not connected")
	}
	if current.lastError != nil {
		return remote.RelayTicket{}, current.lastError
	}
	return manager.gateway.IssueRelayTicket(ctx, current.profile, current.session)
}

func (manager *Manager) RelayTicketSource(profileID string) func(context.Context) (remote.RelayTicket, error) {
	manager.mu.Lock()
	bound, ok := manager.active[profileID]
	manager.mu.Unlock()
	return func(ctx context.Context) (remote.RelayTicket, error) {
		manager.mu.Lock()
		current, active := manager.active[profileID]
		if !ok || !active || current.session.ID != bound.session.ID ||
			current.session.Generation != bound.session.Generation {
			manager.mu.Unlock()
			return remote.RelayTicket{}, errors.New("remote Session generation changed")
		}
		if current.lastError != nil {
			err := current.lastError
			manager.mu.Unlock()
			return remote.RelayTicket{}, err
		}
		manager.mu.Unlock()
		return manager.gateway.IssueRelayTicket(ctx, current.profile, current.session)
	}
}

func (manager *Manager) Shutdown(ctx context.Context) error {
	manager.cancel()
	select {
	case <-manager.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	var result error
	for profileID, current := range manager.active {
		if _, err := manager.gateway.DisconnectSession(
			ctx,
			current.profile,
			current.session,
		); err != nil &&
			!isGone(err) {
			result = errors.Join(result, err)
			continue
		}
		delete(manager.active, profileID)
	}
	return result
}

func (manager *Manager) heartbeatLoop() {
	defer close(manager.done)
	ticker := time.NewTicker(manager.interval)
	defer ticker.Stop()
	for {
		select {
		case <-manager.ctx.Done():
			return
		case <-ticker.C:
			manager.heartbeat()
		}
	}
}

func (manager *Manager) heartbeat() {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	for profileID, current := range manager.active {
		ctx, cancel := context.WithTimeout(manager.ctx, manager.interval)
		next, err := manager.gateway.HeartbeatSession(ctx, current.profile, current.session)
		cancel()
		if err != nil {
			current.lastError = err
			manager.active[profileID] = current
			continue
		}
		current.session = next
		current.lastError = nil
		manager.active[profileID] = current
		manager.publishSessionUpdateLocked(profileID, next)
	}
}

func (manager *Manager) publishSessionUpdateLocked(profileID string, session remote.Session) {
	update := remote.SessionUpdate{ProfileID: profileID, Session: session}
	select {
	case manager.updates <- update:
		return
	default:
	}
	select {
	case <-manager.updates:
	default:
	}
	select {
	case manager.updates <- update:
	default:
	}
}

func isGone(err error) bool {
	var apiError *remote.APIError
	return errors.As(err, &apiError) && apiError.Status == 404
}
