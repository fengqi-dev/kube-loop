package dataplane

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/client/socksbridge"
	"github.com/fengqi-dev/kube-loop/internal/client/traffic"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficstream"
	"github.com/fengqi-dev/kube-loop/internal/singbox"
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
	sessions SessionSource
	config   Config
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
	active   map[string]*managedRuntime
	hostTCP  map[string]socksbridge.HostTCPHandler
	events   chan StatusEvent
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
		events: make(chan StatusEvent, 32),
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

func (manager *Manager) Connect(
	ctx context.Context,
	serverProfile profile.Profile,
	session remote.Session,
) (Status, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	desiredMode := ModeSOCKS
	if current := manager.active[serverProfile.ID]; current != nil {
		status := current.runtime.Status()
		select {
		case <-current.runtime.Done():
			delete(manager.active, serverProfile.ID)
		case <-ctx.Done():
			return Status{}, ctx.Err()
		default:
			if status.SessionID == session.ID && status.NetworkSpecHash == session.NetworkSpecHash {
				if session.Generation < current.session.Generation {
					return Status{}, errors.New("stale Session generation")
				}
				if session.Generation > current.session.Generation && !current.recovering {
					current.recovering = true
					baseline := current.session
					status.State = dataplaneReconnecting
					manager.emit(serverProfile.ID, status, errSessionChanged)
					go manager.recover(serverProfile.ID, current, current.runtime, baseline)
				}
				status = current.runtime.Status()
				if current.recovering {
					status.State = dataplaneReconnecting
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
	runtime, err := Start(
		ctx,
		serverProfile,
		session,
		manager.sessions.RelayTicketSource(serverProfile.ID),
		runtimeConfig(manager.config, serverProfile),
	)
	if err != nil {
		return Status{}, err
	}
	if desiredMode == ModeTUN {
		if _, err := runtime.StartTUN(ctx); err != nil {
			_ = runtime.Close()
			return Status{}, fmt.Errorf("restore TUN after Session change: %w", err)
		}
	}
	if handler := manager.hostTCP[serverProfile.ID]; handler != nil {
		runtime.SetHostTCPHandler(handler)
	}
	entry := &managedRuntime{
		profile: serverProfile, session: session, runtime: runtime, desiredMode: desiredMode,
	}
	manager.active[serverProfile.ID] = entry
	go manager.watch(serverProfile.ID, entry, runtime, runtime.TransportDone())
	manager.emit(serverProfile.ID, runtime.Status(), nil)
	return runtime.Status(), nil
}

// ConnectMode connects the stable SOCKS runtime and atomically applies mode.
func (manager *Manager) ConnectMode(
	ctx context.Context,
	serverProfile profile.Profile,
	session remote.Session,
	mode Mode,
) (Status, error) {
	if err := mode.Validate(); err != nil {
		return Status{}, err
	}
	if _, err := manager.Connect(ctx, serverProfile, session); err != nil {
		return Status{}, err
	}
	status, err := manager.SwitchMode(ctx, serverProfile.ID, mode)
	if err != nil {
		return Status{}, errors.Join(err, manager.Disconnect(serverProfile.ID))
	}
	return status, nil
}

// SwitchMode changes local presentation without replacing the remote Session.
func (manager *Manager) SwitchMode(ctx context.Context, profileID string, mode Mode) (Status, error) {
	if err := mode.Validate(); err != nil {
		return Status{}, err
	}
	if mode == ModeTUN {
		return manager.StartTUN(ctx, profileID)
	}
	return manager.StopTUN(profileID)
}

func runtimeConfig(config Config, serverProfile profile.Profile) Config {
	if serverProfile.SOCKSPort != 0 {
		config.ListenAddress = net.JoinHostPort("127.0.0.1", strconv.Itoa(serverProfile.SOCKSPort))
	}
	return config
}

// SetHostTCPHandler installs a profile-scoped host-side TCP interception
// handler. Native Pod SSH uses it to claim an enabled PodIP:22 before the
// SOCKS bridge forwards ordinary cluster traffic to the Gateway.
func (manager *Manager) SetHostTCPHandler(profileID string, handler socksbridge.HostTCPHandler) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	profileID = strings.TrimSpace(profileID)
	entry := manager.active[profileID]
	if profileID == "" || entry == nil {
		return errors.New("data Plane runtime is not connected")
	}
	if entry.runtime.Status().Mode != ModeTUN {
		return errors.New("tUN must be active for native PodIP SSH")
	}
	manager.hostTCP[profileID] = handler
	entry.runtime.SetHostTCPHandler(handler)
	return nil
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
	manager.emit(profileID, Status{State: "disconnected", Mode: ModeSOCKS}, err)
	return err
}

func (manager *Manager) StartTUN(ctx context.Context, profileID string) (Status, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	entry := manager.active[profileID]
	if entry == nil {
		return Status{}, errors.New("data Plane runtime is not connected")
	}
	if entry.recovering {
		entry.desiredMode = ModeTUN
		return Status{}, errors.New("data Plane runtime is reconnecting")
	}
	status, err := entry.runtime.StartTUN(ctx)
	if err == nil {
		entry.desiredMode = ModeTUN
		manager.emit(profileID, status, nil)
	}
	return status, err
}

func (manager *Manager) StopTUN(profileID string) (Status, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	entry := manager.active[profileID]
	if entry == nil {
		return Status{}, errors.New("data Plane runtime is not connected")
	}
	entry.desiredMode = ModeSOCKS
	if entry.recovering {
		status := entry.runtime.Status()
		status.State = dataplaneReconnecting
		status.Mode = ModeSOCKS
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
		return Status{}, errors.New("data Plane runtime is not connected")
	}
	status := entry.runtime.Status()
	if entry.recovering {
		status.State = dataplaneReconnecting
	}
	return status, nil
}

func (manager *Manager) Metrics(ctx context.Context, profileID string) (singbox.Metrics, error) {
	runtime, err := manager.activeRuntime(profileID)
	if err != nil {
		return singbox.Metrics{}, err
	}
	return runtime.Metrics(ctx)
}

func (manager *Manager) TestConnectivity(ctx context.Context, profileID string) error {
	runtime, err := manager.activeRuntime(profileID)
	if err != nil {
		return err
	}
	return runtime.TestConnectivity(ctx)
}

func (manager *Manager) Logs(ctx context.Context, profileID string) ([]string, error) {
	runtime, err := manager.activeRuntime(profileID)
	if err != nil {
		return nil, err
	}
	return runtime.Logs(ctx)
}

func (manager *Manager) ConfigJSON(profileID string) ([]byte, error) {
	runtime, err := manager.activeRuntime(profileID)
	if err != nil {
		return nil, err
	}
	return runtime.ConfigJSON()
}

func (manager *Manager) UpdateDNSNamespace(ctx context.Context, profileID, namespace string) error {
	runtime, err := manager.activeRuntime(profileID)
	if err != nil {
		return err
	}
	return runtime.UpdateDNSNamespace(ctx, namespace)
}

func (manager *Manager) UpdateHostAliases(
	ctx context.Context, profileID string, aliases []singbox.HostAlias,
) error {
	runtime, err := manager.activeRuntime(profileID)
	if err != nil {
		return err
	}
	return runtime.UpdateHostAliases(ctx, aliases)
}

func (manager *Manager) activeRuntime(profileID string) (*Runtime, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	entry := manager.active[strings.TrimSpace(profileID)]
	if entry == nil || entry.runtime == nil {
		return nil, errors.New("data Plane runtime is not connected")
	}
	return entry.runtime, nil
}

// OpenTrafficStream opens a task-scoped logical stream only when the Profile's
// current Data Plane runtime and authoritative Session still match.
func (manager *Manager) OpenTrafficStream(
	ctx context.Context,
	profileID string,
	mode string,
	taskID string,
) (*trafficstream.FrameConn, error) {
	if ctx == nil {
		return nil, errors.New("traffic stream context is required")
	}
	profileID = strings.TrimSpace(profileID)
	manager.mu.Lock()
	entry := manager.active[profileID]
	if entry == nil || entry.runtime == nil || entry.recovering {
		manager.mu.Unlock()
		return nil, errors.New("data Plane runtime is not connected")
	}
	runtime := entry.runtime
	status := runtime.Status()
	if status.State != dataplaneConnected || status.SessionID != entry.session.ID ||
		status.SessionGeneration != entry.session.Generation {
		manager.mu.Unlock()
		return nil, errors.New("data Plane runtime Session does not match")
	}
	manager.mu.Unlock()
	stream, err := runtime.OpenTrafficStream(ctx, mode, taskID)
	if err != nil {
		return nil, err
	}
	manager.mu.Lock()
	current := manager.active[profileID] == entry && entry.runtime == runtime && !entry.recovering
	manager.mu.Unlock()
	if !current {
		_ = stream.Close()
		return nil, errors.New("data Plane runtime changed while opening Traffic Task stream")
	}
	return stream, nil
}

// Dialer returns a fixed SOCKS endpoint for local feature listeners. The
// endpoint stays stable while the underlying WebSocket transport recovers.
func (manager *Manager) Dialer(profileID string) (traffic.Dialer, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	entry := manager.active[profileID]
	if entry == nil {
		return traffic.Dialer{}, errors.New("data Plane runtime is not connected")
	}
	status := entry.runtime.Status()
	if status.SOCKSAddress == "" || status.State == dataplaneError || status.State == "disconnected" {
		return traffic.Dialer{}, errors.New("data Plane SOCKS endpoint is unavailable")
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
		return errors.New("data Plane runtime is not connected")
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
	status.State = dataplaneReconnecting
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
	status.State = dataplaneReconnecting
	manager.emit(profileID, status, entry.lastError)
	manager.recover(profileID, entry, runtime, baseline)
}

// syncSession applies authoritative heartbeat updates while the transport is
// healthy. Every generation owns a matching RelayTicket, control token and WSS
// pool, so advancing a generation requires one atomic transport replacement.
// A changed NetworkSpec additionally reinstalls TUN after the replacement is
// ready; both paths preserve the stable local SOCKS address.
func (manager *Manager) syncSession(update remote.SessionUpdate) {
	profileID := strings.TrimSpace(update.ProfileID)
	session := update.Session
	manager.mu.Lock()
	entry := manager.active[profileID]
	if profileID == "" || entry == nil || entry.runtime == nil || entry.recovering || manager.ctx.Err() != nil {
		manager.mu.Unlock()
		return
	}
	runtime := entry.runtime
	if err := validateRecoverySession(entry.session, session); err != nil {
		manager.mu.Unlock()
		return
	}
	if session.Generation == entry.session.Generation && session.NetworkSpecHash == entry.session.NetworkSpecHash {
		manager.mu.Unlock()
		return
	}
	entry.recovering = true
	baseline := entry.session
	reason := errSessionChanged
	if session.NetworkSpecHash != entry.session.NetworkSpecHash {
		reason = errNetworkSpecChanged
	}
	manager.mu.Unlock()
	status := runtime.Status()
	status.State = dataplaneReconnecting
	manager.emit(profileID, status, reason)
	go manager.recover(profileID, entry, runtime, baseline)
}

func (manager *Manager) sessionUpdateLoop(updates <-chan remote.SessionUpdate) {
	for {
		select {
		case update, ok := <-updates:
			if !ok {
				return
			}
			manager.syncSession(update)
		case <-manager.ctx.Done():
			return
		}
	}
}

func (manager *Manager) recover(profileID string, entry *managedRuntime, failed *Runtime, baseline remote.Session) {
	var lastError error
	for attempt := range manager.config.RecoveryAttempts {
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
		lastError = errors.New("data Plane recovery attempts exhausted")
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
		status.State = dataplaneError
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
		switch {
		case errors.Is(err, errSystemResumed):
			event.Reason = reasonSystemResumed
			event.Retryable = true
		case errors.Is(err, errNetworkSpecChanged):
			event.Reason = reasonNetworkSpecChanged
			event.Retryable = true
		case errors.Is(err, errSessionChanged):
			event.Reason = reasonSessionChanged
			event.Retryable = true
		}
		switch status.State {
		case dataplaneReconnecting:
			if event.Reason == "" {
				event.Reason = reasonTransportInterrupted
				event.Retryable = true
			}
		case dataplaneError:
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
	if apiError, ok := errors.AsType[*remote.APIError](err); ok {
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
	if current.State != dataplaneSessionActive || current.ID != previous.ID {
		return errors.New("active Session identity changed during Data Plane recovery")
	}
	if current.Generation < previous.Generation {
		return errors.New("stale Session generation returned during Data Plane recovery")
	}
	return nil
}

func unrecoverableSessionError(err error) bool {
	if apiError, ok := errors.AsType[*remote.APIError](err); ok {
		return apiError.Status == 401 || apiError.Status == 403 || apiError.Status == 404
	}
	return false
}
