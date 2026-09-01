package dataplane

import (
	"context"
	"errors"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/client/traffic"
	"github.com/fengqi-dev/kube-loop/internal/singbox"
	"github.com/fengqi-dev/kube-loop/internal/transport/trafficstream"
)

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
	if manager.closed.Load() {
		return ErrClosed
	}
	manager.lifecycle.RLock()
	defer manager.lifecycle.RUnlock()
	if manager.closed.Load() {
		return ErrClosed
	}
	operation := manager.profileOperation(profileID)
	operation.Lock()
	defer operation.Unlock()
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
	manager.workers.Go(func() {
		manager.recover(profileID, entry, runtime, baseline)
	})
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
	manager.closed.Store(true)
	manager.cancel()
	manager.lifecycle.Lock()
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
	manager.lifecycle.Unlock()
	manager.workers.Wait()
	return result
}
