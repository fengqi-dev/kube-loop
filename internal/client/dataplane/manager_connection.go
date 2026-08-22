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
