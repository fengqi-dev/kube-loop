package session

import (
	"errors"
	"fmt"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/singbox"
)

func (m *Manager) Disconnect() error {
	m.recordLog("INFO", "disconnect requested")
	return m.disconnect(true)
}

// Shutdown persists restore intents, then tears down runtime without clearing
// the "was connected" flag used for next-launch recovery.
func (m *Manager) Shutdown() error {
	m.recordLog("INFO", "application shutdown started; preserving session restore state")
	m.mu.Lock()
	m.shuttingDown = true
	m.mu.Unlock()
	m.PersistShutdown()
	m.StopAllPortForwards()
	return m.disconnect(false)
}

func (m *Manager) disconnect(clearConnected bool) error {
	state := m.State()
	m.clearRecentConnections()
	m.mu.RLock()
	cancel, done := m.cancel, m.done
	m.mu.RUnlock()
	if cancel == nil {
		m.publish(State{
			Phase:             PhaseIdle,
			Mode:              state.Mode,
			Message:           "Disconnected",
			CoreVersion:       singbox.Version,
			KubernetesVersion: state.KubernetesVersion,
			Context:           state.Context,
			Namespace:         state.Namespace,
		})
		if clearConnected {
			m.markDisconnected(state.Context, state.Namespace)
		}
		return nil
	}
	cancel()
	select {
	case <-done:
		if clearConnected {
			m.markDisconnected(state.Context, state.Namespace)
		}
		return nil
	case <-time.After(25 * time.Second):
		m.recordLog("ERROR", "timed out cleaning up the active connection")
		return errors.New("timed out cleaning up the active connection")
	}
}

func (m *Manager) markDisconnected(contextName, namespace string) {
	if m.store == nil || contextName == "" {
		return
	}
	if err := m.store.SetConnected(contextName, namespace, false); err != nil {
		m.AppendLog("ERROR", fmt.Sprintf("persist disconnected state: %v", err))
	}
}
