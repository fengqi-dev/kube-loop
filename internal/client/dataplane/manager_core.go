package dataplane

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/client/socksbridge"
)

type SessionSource interface {
	RelayTicketSource(string) func(context.Context) (remote.RelayTicket, error)
	Refresh(context.Context, string) (remote.Session, error)
}

type sessionUpdateSource interface {
	SessionUpdates() <-chan remote.SessionUpdate
}

type managedRuntime struct {
	profile     profile.Profile
	session     remote.Session
	runtime     *Runtime
	desiredMode string
	recovering  bool
	lastError   error
}

const (
	reasonTransportInterrupted   = "transport_interrupted"
	reasonNetworkSpecChanged     = "network_spec_changed"
	reasonAuthenticationRequired = "authentication_required"
	reasonAccessDenied           = "access_denied"
	reasonSessionExpired         = "session_expired"
	reasonSessionChanged         = "session_changed"
	reasonNetworkUnavailable     = "network_unavailable"
	reasonSystemResumed          = "system_resumed"
)

var (
	errSystemResumed      = errors.New("system resumed")
	errNetworkSpecChanged = errors.New("session NetworkSpec changed")
	errSessionChanged     = errors.New("session generation changed")
)

type Manager struct {
	sessions   SessionSource
	config     Config
	ctx        context.Context
	cancel     context.CancelFunc
	lifecycle  sync.RWMutex
	mu         sync.Mutex
	active     map[string]*managedRuntime
	hostTCP    map[string]socksbridge.HostTCPHandler
	operations map[string]*sync.Mutex
	events     chan StatusEvent
}

// Mode identifies how local applications enter the connected data plane.
type Mode string

const (
	ModeSOCKS = "socks"
	ModeTUN   = "tun"
)

func (mode Mode) Validate() error {
	if mode != ModeSOCKS && mode != ModeTUN {
		return errors.New("data Plane mode must be socks or tun")
	}
	return nil
}

func NewManager(sessions SessionSource, config Config) (*Manager, error) {
	if sessions == nil {
		return nil, errors.New("data Plane Session source is required")
	}
	if config.RecoveryAttempts <= 0 {
		config.RecoveryAttempts = DefaultRecoveryAttempts
	}
	if config.RecoveryAttempts > 10 {
		return nil, errors.New("data Plane recovery attempts must not exceed 10")
	}
	if config.RecoveryBackoff <= 0 {
		config.RecoveryBackoff = DefaultRecoveryBackoff
	}
	if config.RecoveryBackoff > 30*time.Second {
		return nil, errors.New("data Plane recovery backoff must not exceed 30 seconds")
	}
	ctx, cancel := context.WithCancel(context.Background())
	manager := &Manager{
		sessions: sessions, config: config, ctx: ctx, cancel: cancel,
		active: make(map[string]*managedRuntime), hostTCP: make(map[string]socksbridge.HostTCPHandler),
		operations: make(map[string]*sync.Mutex),
		events:     make(chan StatusEvent, 32),
	}
	if config.OnStatus != nil {
		go manager.eventLoop(config.OnStatus)
	}
	if source, ok := sessions.(sessionUpdateSource); ok {
		if updates := source.SessionUpdates(); updates != nil {
			go manager.sessionUpdateLoop(updates)
		}
	}
	return manager, nil
}

func (manager *Manager) profileOperation(profileID string) *sync.Mutex {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.operations == nil {
		manager.operations = make(map[string]*sync.Mutex)
	}
	operation := manager.operations[profileID]
	if operation == nil {
		operation = &sync.Mutex{}
		manager.operations[profileID] = operation
	}
	return operation
}
