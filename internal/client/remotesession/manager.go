package remotesession

import (
	"context"
	"errors"
	"maps"
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
	lifecycle   sync.RWMutex
	mu          sync.Mutex
	active      map[string]entry
	pendingKeys map[string]string
	operations  map[string]*sync.Mutex
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
		operations: make(map[string]*sync.Mutex),
		updates:    make(chan remote.SessionUpdate, sessionUpdateBuffer), done: make(chan struct{}),
	}
	go manager.heartbeatLoop()
	return manager, nil
}

func (manager *Manager) Connect(
	ctx context.Context,
	serverProfile profile.Profile,
	namespace string,
) (remote.Session, error) {
	manager.lifecycle.RLock()
	defer manager.lifecycle.RUnlock()
	operation := manager.profileOperation(serverProfile.ID)
	operation.Lock()
	defer operation.Unlock()

	manager.mu.Lock()
	current, ok := manager.active[serverProfile.ID]
	manager.mu.Unlock()
	if ok {
		if current.session.Namespace == namespace && current.session.State == remoteSessionActive {
			if current.lastError == nil {
				return current.session, nil
			}
			if !isGone(current.lastError) {
				return current.session, current.lastError
			}
			manager.mu.Lock()
			delete(manager.active, serverProfile.ID)
			manager.mu.Unlock()
		} else {
			if _, err := manager.gateway.DisconnectSession(
				ctx,
				current.profile,
				current.session,
			); err != nil &&
				!isGone(err) {
				return remote.Session{}, err
			}
			manager.mu.Lock()
			delete(manager.active, serverProfile.ID)
			manager.mu.Unlock()
		}
	}
	pendingID := serverProfile.ID + "\x00" + namespace
	manager.mu.Lock()
	idempotencyKey := manager.pendingKeys[pendingID]
	if idempotencyKey == "" {
		idempotencyKey = "desktop-" + uuid.NewString()
		manager.pendingKeys[pendingID] = idempotencyKey
	}
	manager.mu.Unlock()
	session, err := manager.gateway.CreateSession(ctx, serverProfile, namespace, idempotencyKey)
	if err != nil {
		return remote.Session{}, err
	}
	manager.mu.Lock()
	delete(manager.pendingKeys, pendingID)
	manager.active[serverProfile.ID] = entry{profile: serverProfile, session: session}
	manager.mu.Unlock()
	return session, nil
}

func (manager *Manager) Disconnect(ctx context.Context, profileID string) error {
	manager.lifecycle.RLock()
	defer manager.lifecycle.RUnlock()
	operation := manager.profileOperation(profileID)
	operation.Lock()
	defer operation.Unlock()

	manager.mu.Lock()
	current, ok := manager.active[profileID]
	manager.mu.Unlock()
	if !ok {
		return nil
	}
	_, err := manager.gateway.DisconnectSession(ctx, current.profile, current.session)
	if err != nil && !isGone(err) {
		return err
	}
	manager.mu.Lock()
	delete(manager.active, profileID)
	manager.mu.Unlock()
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

func (manager *Manager) Shutdown(ctx context.Context) error {
	manager.cancel()
	select {
	case <-manager.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	manager.lifecycle.Lock()
	defer manager.lifecycle.Unlock()
	manager.mu.Lock()
	active := maps.Clone(manager.active)
	manager.mu.Unlock()
	var result error
	for profileID, current := range active {
		if _, err := manager.gateway.DisconnectSession(
			ctx,
			current.profile,
			current.session,
		); err != nil &&
			!isGone(err) {
			result = errors.Join(result, err)
			continue
		}
		manager.mu.Lock()
		delete(manager.active, profileID)
		manager.mu.Unlock()
	}
	return result
}

func (manager *Manager) profileOperation(profileID string) *sync.Mutex {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	operation := manager.operations[profileID]
	if operation == nil {
		operation = &sync.Mutex{}
		manager.operations[profileID] = operation
	}
	return operation
}

func isGone(err error) bool {
	var apiError *remote.APIError
	return errors.As(err, &apiError) && apiError.Status == 404
}
