package dataplane

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/client/socksbridge"
)

func (manager *Manager) Connect(
	ctx context.Context,
	serverProfile profile.Profile,
	session remote.Session,
) (Status, error) {
	manager.lifecycle.RLock()
	defer manager.lifecycle.RUnlock()
	operation := manager.profileOperation(serverProfile.ID)
	operation.Lock()
	defer operation.Unlock()

	manager.mu.Lock()
	current := manager.active[serverProfile.ID]
	manager.mu.Unlock()
	desiredMode := ModeSOCKS
	if current != nil {
		status := current.runtime.Status()
		select {
		case <-current.runtime.Done():
			manager.mu.Lock()
			delete(manager.active, serverProfile.ID)
			manager.mu.Unlock()
		case <-ctx.Done():
			return Status{}, ctx.Err()
		default:
			if status.SessionID == session.ID && status.NetworkSpecHash == session.NetworkSpecHash {
				manager.mu.Lock()
				if session.Generation < current.session.Generation {
					manager.mu.Unlock()
					return Status{}, errors.New("stale Session generation")
				}
				if session.Generation > current.session.Generation && !current.recovering {
					current.recovering = true
					baseline := current.session
					status.State = dataplaneReconnecting
					manager.emit(serverProfile.ID, status, errSessionChanged)
					go manager.recover(serverProfile.ID, current, current.runtime, baseline)
				}
				recovering := current.recovering
				manager.mu.Unlock()
				status = current.runtime.Status()
				if recovering {
					status.State = dataplaneReconnecting
				}
				return status, nil
			}
			desiredMode = current.desiredMode
			manager.mu.Lock()
			delete(manager.active, serverProfile.ID)
			manager.mu.Unlock()
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
	manager.mu.Lock()
	handler := manager.hostTCP[serverProfile.ID]
	manager.mu.Unlock()
	if handler != nil {
		runtime.SetHostTCPHandler(handler)
	}
	entry := &managedRuntime{
		profile: serverProfile, session: session, runtime: runtime, desiredMode: desiredMode,
	}
	manager.mu.Lock()
	manager.active[serverProfile.ID] = entry
	manager.mu.Unlock()
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
	profileID = strings.TrimSpace(profileID)
	manager.lifecycle.RLock()
	defer manager.lifecycle.RUnlock()
	operation := manager.profileOperation(profileID)
	operation.Lock()
	defer operation.Unlock()
	manager.mu.Lock()
	entry := manager.active[profileID]
	manager.mu.Unlock()
	if profileID == "" || entry == nil {
		return errors.New("data Plane runtime is not connected")
	}
	if entry.runtime.Status().Mode != ModeTUN {
		return errors.New("tUN must be active for native PodIP SSH")
	}
	manager.mu.Lock()
	manager.hostTCP[profileID] = handler
	manager.mu.Unlock()
	entry.runtime.SetHostTCPHandler(handler)
	return nil
}

func (manager *Manager) Disconnect(profileID string) error {
	manager.lifecycle.RLock()
	defer manager.lifecycle.RUnlock()
	operation := manager.profileOperation(profileID)
	operation.Lock()
	defer operation.Unlock()
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
	manager.lifecycle.RLock()
	defer manager.lifecycle.RUnlock()
	operation := manager.profileOperation(profileID)
	operation.Lock()
	defer operation.Unlock()
	manager.mu.Lock()
	entry := manager.active[profileID]
	if entry == nil {
		manager.mu.Unlock()
		return Status{}, errors.New("data Plane runtime is not connected")
	}
	if entry.recovering {
		entry.desiredMode = ModeTUN
		manager.mu.Unlock()
		return Status{}, errors.New("data Plane runtime is reconnecting")
	}
	manager.mu.Unlock()
	status, err := entry.runtime.StartTUN(ctx)
	if err == nil {
		manager.mu.Lock()
		entry.desiredMode = ModeTUN
		manager.mu.Unlock()
		manager.emit(profileID, status, nil)
	}
	return status, err
}

func (manager *Manager) StopTUN(profileID string) (Status, error) {
	manager.lifecycle.RLock()
	defer manager.lifecycle.RUnlock()
	operation := manager.profileOperation(profileID)
	operation.Lock()
	defer operation.Unlock()
	manager.mu.Lock()
	entry := manager.active[profileID]
	if entry == nil {
		manager.mu.Unlock()
		return Status{}, errors.New("data Plane runtime is not connected")
	}
	entry.desiredMode = ModeSOCKS
	if entry.recovering {
		manager.mu.Unlock()
		status := entry.runtime.Status()
		status.State = dataplaneReconnecting
		status.Mode = ModeSOCKS
		manager.emit(profileID, status, nil)
		return status, nil
	}
	manager.mu.Unlock()
	status, err := entry.runtime.StopTUN()
	manager.emit(profileID, status, err)
	return status, err
}

// Status returns the current Data Plane state without changing the Runtime or
// publishing another status event. It is suitable for UI refreshes and health
// polling that must not retrigger lifecycle operations.
