package remotesession

import (
	"context"
	"errors"
	"sync"
	"time"

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
