package dataplane

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/clientv2/profile"
	"github.com/fengqi-dev/kube-loop/internal/clientv2/remote"
	"github.com/fengqi-dev/kube-loop/internal/traffic"
)

type SessionSource interface {
	RelayTicketSource(string) func(context.Context) (remote.RelayTicket, error)
	Current(string) (remote.Session, error)
	Refresh(context.Context, string) (remote.Session, error)
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
	reasonAuthenticationRequired = "authentication_required"
	reasonAccessDenied           = "access_denied"
	reasonSessionExpired         = "session_expired"
	reasonSessionChanged         = "session_changed"
	reasonNetworkUnavailable     = "network_unavailable"
	reasonSystemResumed          = "system_resumed"
)

var errSystemResumed = errors.New("system resumed")

type Manager struct {
	sessions SessionSource
	config   Config
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
	active   map[string]*managedRuntime
	events   chan StatusEvent
}

func NewManager(sessions SessionSource, config Config) (*Manager, error) {
	if sessions == nil {
		return nil, errors.New("Data Plane Session source is required")
	}
	if config.RecoveryAttempts <= 0 {
		config.RecoveryAttempts = DefaultRecoveryAttempts
	}
	if config.RecoveryAttempts > 10 {
		return nil, errors.New("Data Plane recovery attempts must not exceed 10")
	}
	if config.RecoveryBackoff <= 0 {
		config.RecoveryBackoff = DefaultRecoveryBackoff
	}
	if config.RecoveryBackoff > 30*time.Second {
		return nil, errors.New("Data Plane recovery backoff must not exceed 30 seconds")
	}
	ctx, cancel := context.WithCancel(context.Background())
	manager := &Manager{
		sessions: sessions, config: config, ctx: ctx, cancel: cancel,
		active: make(map[string]*managedRuntime), events: make(chan StatusEvent, 32),
	}
	if config.OnStatus != nil {
		go manager.eventLoop(config.OnStatus)
	}
	return manager, nil
}

func (manager *Manager) Connect(ctx context.Context, serverProfile profile.Profile, session remote.Session) (Status, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	desiredMode := "socks"
	if current := manager.active[serverProfile.ID]; current != nil {
		status := current.runtime.Status()
		select {
		case <-current.runtime.Done():
			delete(manager.active, serverProfile.ID)
		case <-ctx.Done():
			return Status{}, ctx.Err()
		default:
			if status.SessionID == session.ID && status.NetworkSpecHash == session.NetworkSpecHash {
				if session.Generation >= current.session.Generation {
					if err := current.runtime.AdvanceSession(session); err != nil {
						return Status{}, err
					}
					current.session = session
				}
				status = current.runtime.Status()
				if current.recovering {
					status.State = "reconnecting"
				}
				return status, nil
			}
			desiredMode = current.desiredMode
			delete(manager.active, serverProfile.ID)
			if err := current.runtime.Close(); err != nil {
				return Status{}, err
			}
		}
	}
	runtime, err := Start(ctx, serverProfile, session, manager.sessions.RelayTicketSource(serverProfile.ID), manager.config)
	if err != nil {
		return Status{}, err
	}
	if desiredMode == "tun" {
		if _, err := runtime.StartTUN(ctx); err != nil {
			_ = runtime.Close()
			return Status{}, fmt.Errorf("restore V2 TUN after Session change: %w", err)
		}
	}
	entry := &managedRuntime{
		profile: serverProfile, session: session, runtime: runtime, desiredMode: desiredMode,
	}
	manager.active[serverProfile.ID] = entry
	go manager.watch(serverProfile.ID, entry, runtime, runtime.TransportDone())
	manager.emit(serverProfile.ID, runtime.Status(), nil)
	return runtime.Status(), nil
}

func (manager *Manager) Disconnect(profileID string) error {
	manager.mu.Lock()
	current := manager.active[profileID]
	if current != nil {
		delete(manager.active, profileID)
	}
	manager.mu.Unlock()
	if current == nil {
		return nil
	}
	err := current.runtime.Close()
	manager.emit(profileID, Status{State: "disconnected", Mode: "socks"}, err)
	return err
}

func (manager *Manager) StartTUN(ctx context.Context, profileID string) (Status, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	entry := manager.active[profileID]
	if entry == nil {
		return Status{}, errors.New("Data Plane runtime is not connected")
	}
	if entry.recovering {
		entry.desiredMode = "tun"
		return Status{}, errors.New("Data Plane runtime is reconnecting")
	}
	status, err := entry.runtime.StartTUN(ctx)
	if err == nil {
		entry.desiredMode = "tun"
		manager.emit(profileID, status, nil)
	}
	return status, err
}

func (manager *Manager) StopTUN(profileID string) (Status, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	entry := manager.active[profileID]
	if entry == nil {
		return Status{}, errors.New("Data Plane runtime is not connected")
	}
	entry.desiredMode = "socks"
	if entry.recovering {
		status := entry.runtime.Status()
		status.State = "reconnecting"
		status.Mode = "socks"
		manager.emit(profileID, status, nil)
		return status, nil
	}
	status, err := entry.runtime.StopTUN()
	manager.emit(profileID, status, err)
	return status, err
}

// Status returns the current Data Plane state without changing the Runtime or
// publishing another status event. It is suitable for UI refreshes and health
// polling that must not retrigger lifecycle operations.
func (manager *Manager) Status(profileID string) (Status, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	entry := manager.active[profileID]
	if entry == nil {
		return Status{}, errors.New("Data Plane runtime is not connected")
	}
	status := entry.runtime.Status()
	if entry.recovering {
		status.State = "reconnecting"
	}
	return status, nil
}

// Dialer returns a fixed SOCKS endpoint for local feature listeners. The
// endpoint stays stable while the underlying WebSocket transport recovers.
func (manager *Manager) Dialer(profileID string) (traffic.Dialer, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	entry := manager.active[profileID]
	if entry == nil {
		return traffic.Dialer{}, errors.New("Data Plane runtime is not connected")
	}
	status := entry.runtime.Status()
	if status.SOCKSAddress == "" || status.State == "error" || status.State == "disconnected" {
		return traffic.Dialer{}, errors.New("Data Plane SOCKS endpoint is unavailable")
	}
	return traffic.Dialer{Endpoint: traffic.Endpoint{Address: status.SOCKSAddress}}, nil
}

// Resume proactively replaces the transport after an operating-system wake.
// Sleeping hosts can retain a TCP socket that appears open until the next
// write, so waiting for TransportDone would leave TUN traffic black-holed.
// The Runtime keeps its stable SOCKS listener and active TUN while the
// authoritative Session and RelayTicket are refreshed.
func (manager *Manager) Resume(profileID string) error {
	manager.mu.Lock()
	entry := manager.active[profileID]
	if entry == nil {
		manager.mu.Unlock()
		return errors.New("Data Plane runtime is not connected")
	}
	if entry.recovering {
		manager.mu.Unlock()
		return nil
	}
	entry.recovering = true
	runtime := entry.runtime
	baseline := entry.session
	manager.mu.Unlock()
	status := runtime.Status()
	status.State = "reconnecting"
	manager.emit(profileID, status, errSystemResumed)
	runtime.interruptTransport(errSystemResumed)
	go manager.recover(profileID, entry, runtime, baseline)
	return nil
}

// ResumeAll schedules one coalesced wake recovery for every active Profile.
// It returns the number of connected Profiles that were present at the time
// of the notification, including Profiles already recovering.
func (manager *Manager) ResumeAll() int {
	manager.mu.Lock()
	profileIDs := make([]string, 0, len(manager.active))
	for profileID := range manager.active {
		profileIDs = append(profileIDs, profileID)
	}
	manager.mu.Unlock()
	for _, profileID := range profileIDs {
		_ = manager.Resume(profileID)
	}
	return len(profileIDs)
}

func (manager *Manager) Shutdown() error {
	manager.cancel()
	manager.mu.Lock()
	entries := make([]*managedRuntime, 0, len(manager.active))
	for profileID, entry := range manager.active {
		entries = append(entries, entry)
		delete(manager.active, profileID)
	}
	manager.mu.Unlock()
	var result error
	for _, entry := range entries {
		result = errors.Join(result, entry.runtime.Close())
	}
	return result
}

func (manager *Manager) watch(
	profileID string,
	entry *managedRuntime,
	runtime *Runtime,
	transportDone <-chan struct{},
) {
	select {
	case <-transportDone:
	case <-runtime.Done():
		return
	case <-manager.ctx.Done():
		return
	}
	select {
	case <-runtime.Done():
		manager.mu.Lock()
		if manager.active[profileID] == entry && entry.runtime == runtime {
			entry.lastError = runtime.Err()
		}
		manager.mu.Unlock()
		return
	default:
	}
	manager.mu.Lock()
	if manager.active[profileID] != entry || entry.runtime != runtime || manager.ctx.Err() != nil ||
		runtime.TransportDone() != transportDone {
		manager.mu.Unlock()
		return
	}
	if entry.recovering {
		manager.mu.Unlock()
		return
	}
	entry.recovering = true
	entry.lastError = runtime.TransportErr()
	baseline := entry.session
	manager.mu.Unlock()
	status := runtime.Status()
	status.State = "reconnecting"
	manager.emit(profileID, status, entry.lastError)
	manager.recover(profileID, entry, runtime, baseline)
}

func (manager *Manager) recover(profileID string, entry *managedRuntime, failed *Runtime, baseline remote.Session) {
	var lastError error
	for attempt := 0; attempt < manager.config.RecoveryAttempts; attempt++ {
		if attempt > 0 && !manager.waitRecovery(attempt) {
			return
		}
		session, err := manager.sessions.Refresh(manager.ctx, profileID)
		if err == nil {
			err = validateRecoverySession(baseline, session)
		}
		if err != nil {
			lastError = err
			if unrecoverableSessionError(err) {
				break
			}
			continue
		}
		manager.mu.Lock()
		valid := manager.active[profileID] == entry && entry.runtime == failed && manager.ctx.Err() == nil
		manager.mu.Unlock()
		if !valid {
			return
		}
		err = failed.Reconnect(manager.ctx, entry.profile, session, manager.sessions.RelayTicketSource(profileID))
		if err != nil {
			lastError = err
			continue
		}
		manager.mu.Lock()
		if manager.active[profileID] != entry || entry.runtime != failed || manager.ctx.Err() != nil ||
			session.Generation < entry.session.Generation {
			manager.mu.Unlock()
			return
		}
		entry.session = session
		entry.recovering = false
		entry.lastError = nil
		manager.mu.Unlock()
		manager.emit(profileID, failed.Status(), nil)
		go manager.watch(profileID, entry, failed, failed.TransportDone())
		return
	}
	if lastError == nil {
		lastError = errors.New("Data Plane recovery attempts exhausted")
	}
	manager.mu.Lock()
	shouldClose := manager.active[profileID] == entry && entry.runtime == failed
	manager.mu.Unlock()
	if shouldClose {
		closeError := failed.Close()
		terminalError := fmt.Errorf("recover Data Plane: %w", lastError)
		if closeError != nil {
			terminalError = errors.Join(terminalError, fmt.Errorf("close failed Data Plane: %w", closeError))
		}
		manager.mu.Lock()
		stillActive := manager.active[profileID] == entry && entry.runtime == failed
		if stillActive {
			entry.recovering = false
			entry.lastError = terminalError
		}
		manager.mu.Unlock()
		if !stillActive {
			return
		}
		status := failed.Status()
		status.State = "error"
		manager.emit(profileID, status, terminalError)
	}
}
func (manager *Manager) emit(profileID string, status Status, err error) {
	if manager.config.OnStatus == nil {
		return
	}
	event := StatusEvent{ProfileID: profileID, Status: status}
	if err != nil {
		event.Error = err.Error()
		if errors.Is(err, errSystemResumed) {
			event.Reason = reasonSystemResumed
			event.Retryable = true
		}
		switch status.State {
		case "reconnecting":
			if event.Reason == "" {
				event.Reason = reasonTransportInterrupted
				event.Retryable = true
			}
		case "error":
			event.Reason, event.Retryable = recoveryFailureAction(err)
		}
	}
	select {
	case manager.events <- event:
		return
	case <-manager.ctx.Done():
		return
	default:
	}
	// A UI callback is outside the Data Plane trust boundary and may stall.
	// Preserve lifecycle progress by coalescing a full queue toward its newest
	// status instead of blocking while a Manager mutex may be held.
	select {
	case <-manager.events:
	default:
	}
	select {
	case manager.events <- event:
	case <-manager.ctx.Done():
	default:
	}
}

func recoveryFailureAction(err error) (string, bool) {
	var apiError *remote.APIError
	if errors.As(err, &apiError) {
		switch apiError.Status {
		case 401:
			return reasonAuthenticationRequired, false
		case 403:
			return reasonAccessDenied, false
		case 404:
			return reasonSessionExpired, true
		}
	}
	message := err.Error()
	if strings.Contains(message, "Session identity changed") || strings.Contains(message, "stale Session generation") {
		return reasonSessionChanged, true
	}
	return reasonNetworkUnavailable, true
}

func (manager *Manager) eventLoop(callback func(StatusEvent)) {
	for {
		select {
		case event := <-manager.events:
			callback(event)
		case <-manager.ctx.Done():
			return
		}
	}
}

func (manager *Manager) waitRecovery(attempt int) bool {
	delay := manager.config.RecoveryBackoff
	for step := 1; step < attempt && delay < 30*time.Second; step++ {
		delay *= 2
		if delay > 30*time.Second {
			delay = 30 * time.Second
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-manager.ctx.Done():
		return false
	}
}

func validateRecoverySession(previous, current remote.Session) error {
	if current.State != "active" || current.ID != previous.ID {
		return errors.New("active Session identity changed during Data Plane recovery")
	}
	if current.Generation < previous.Generation {
		return errors.New("stale Session generation returned during Data Plane recovery")
	}
	return nil
}

func unrecoverableSessionError(err error) bool {
	var apiError *remote.APIError
	if errors.As(err, &apiError) {
		return apiError.Status == 401 || apiError.Status == 403 || apiError.Status == 404
	}
	return false
}
