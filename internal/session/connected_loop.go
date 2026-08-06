package session

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/cluster"
	"github.com/fengqi-dev/kube-loop/internal/singbox"
)

const controlRecoveryAttempts = 5

// serveConnected monitors the long-lived parts of an established session.
// Setup and resource ownership remain in run; this loop only reacts to
// cancellation, core exit, control-channel loss, and metrics ticks.
func (m *Manager) serveConnected(
	ctx context.Context,
	state State,
	core singbox.RunningCore,
	contextName string,
	bridge any,
	runtime *sessionRuntime,
) {
	ticker := time.NewTicker(singbox.DefaultMetricsInterval)
	defer ticker.Stop()

	controlLost := m.intercept.ControlLost()
	for {
		select {
		case <-ctx.Done():
			m.flushSingBoxLogs(core)
			return

		case <-core.Done():
			m.flushSingBoxLogs(core)
			m.clearRecentConnections()
			if ctx.Err() == nil {
				err := core.Err()
				if err == nil {
					err = errors.New("sing-box stopped unexpectedly")
				}
				m.fail(ctx, state, "sing-box TUN exited unexpectedly", err)
			}
			return

		case <-controlLost:
			if ctx.Err() != nil {
				m.clearRecentConnections()
				return
			}
			m.AppendLog("WARN", "gateway control channel closed; reconnecting")
			if err := m.recoverGatewayControl(
				ctx, contextName, bridge, runtime,
			); err != nil {
				m.clearRecentConnections()
				if ctx.Err() == nil {
					m.fail(ctx, state, "Gateway control channel closed; reconnect required", err)
				}
				return
			}
			m.AppendLog("INFO", "gateway control channel restored")
			controlLost = m.intercept.ControlLost()

		case <-ticker.C:
			m.collectSingBoxLogs(ctx, core)
			m.updateCoreMetrics(ctx, core)
		}
	}
}

func (m *Manager) flushSingBoxLogs(core singbox.RunningCore) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	m.collectSingBoxLogs(ctx, core)
}

func (m *Manager) serveSOCKSConnected(
	ctx context.Context,
	state State,
	contextName string,
	bridge any,
	runtime *sessionRuntime,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.intercept.ControlLost():
			if ctx.Err() != nil {
				return
			}
			m.AppendLog("WARN", "gateway control channel closed; reconnecting")
			if err := m.recoverGatewayControl(
				ctx, contextName, bridge, runtime,
			); err != nil {
				if ctx.Err() == nil {
					m.fail(
						ctx,
						state,
						"Gateway control channel closed; reconnect required",
						err,
					)
				}
				return
			}
			m.AppendLog("INFO", "gateway control channel restored")
		}
	}
}

func (m *Manager) recoverGatewayControl(
	ctx context.Context,
	contextName string,
	bridge any,
	runtime *sessionRuntime,
) error {
	var lastErr error
	for attempt := range controlRecoveryAttempts {
		if delay := controlRecoveryDelay(attempt); delay > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
		if attempt == 0 {
			lastErr = m.intercept.RecoverControl(ctx)
		} else {
			lastErr = m.replaceGatewayPortForward(ctx, contextName, bridge, runtime)
		}
		if lastErr == nil {
			return nil
		}
		m.AppendLog("WARN", fmt.Sprintf(
			"gateway control reconnect attempt %d/%d failed: %v",
			attempt+1,
			controlRecoveryAttempts,
			lastErr,
		))
	}
	if lastErr == nil {
		lastErr = errors.New("gateway control channel closed")
	}
	return lastErr
}

func (m *Manager) replaceGatewayPortForward(
	ctx context.Context,
	contextName string,
	bridge any,
	runtime *sessionRuntime,
) error {
	gateway, err := m.gateway.GetGateway(ctx, contextName)
	if err != nil {
		return fmt.Errorf("find replacement Gateway: %w", err)
	}
	forwarder, err := m.gateway.StartPortForward(
		ctx, contextName, gateway.Name, cluster.GatewayPort,
	)
	if err != nil {
		return fmt.Errorf("replace Gateway port-forward: %w", err)
	}
	if err := m.intercept.RecoverControlAt(
		ctx, gateway.IP, forwarder.Address(),
	); err != nil {
		_ = forwarder.Close()
		return err
	}
	updater, ok := bridge.(interface{ SetGatewayAddress(string) })
	if !ok {
		_ = forwarder.Close()
		return errors.New("SOCKS bridge cannot replace its Gateway address")
	}
	updater.SetGatewayAddress(forwarder.Address())
	runtime.Add("replacement Gateway port-forward", forwarder)
	return nil
}

func controlRecoveryDelay(attempt int) time.Duration {
	switch {
	case attempt <= 0:
		return 0
	case attempt >= 3:
		return 2 * time.Second
	default:
		return time.Second
	}
}

func (m *Manager) updateCoreMetrics(ctx context.Context, core singbox.RunningCore) {
	metrics, err := core.Snapshot(ctx)
	if err != nil {
		return
	}
	m.mu.RLock()
	tracker := m.trafficTracker
	m.mu.RUnlock()
	if m.State().Phase != PhaseConnected {
		return
	}
	metrics = mergeTrafficTracker(metrics, tracker)
	retained := m.retainMetrics(metrics)
	m.stateHub.mu.Lock()
	if m.stateHub.state.Phase != PhaseConnected {
		m.stateHub.mu.Unlock()
		return
	}
	m.stateHub.state.Metrics = retained
	m.stateHub.state.UpdatedAt = time.Now()
	m.stateHub.mu.Unlock()
	m.publishMetrics(retained)
}
