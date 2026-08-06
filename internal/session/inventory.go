package session

import (
	"context"

	"github.com/fengqi-dev/kube-loop/internal/cluster"
)

func (m *Manager) applyInventory(snap cluster.InventorySnapshot) {
	m.stateHub.mu.Lock()
	if m.stateHub.state.Phase != PhaseConnected || m.stateHub.state.Discovery == nil {
		m.stateHub.mu.Unlock()
		return
	}
	next := m.stateHub.state
	discovery := *next.Discovery
	discovery.Pods = snap.Pods
	discovery.Services = snap.Services
	discovery.Deployments = snap.Deployments
	discovery.ServiceIPs = append([]string{}, snap.ServiceIPs...)
	if snap.DNSServer != "" {
		discovery.DNSServer = snap.DNSServer
	}
	next.Discovery = &discovery
	next.Pods = append([]cluster.PodInfo{}, snap.PodItems...)
	next.Services = append([]cluster.ServiceInfo{}, snap.ServiceItems...)
	next.InventoryRevision++
	m.stateHub.state = next
	m.stateHub.mu.Unlock()

	ctx := context.Background()
	m.syncDefaultPodSSH(next, snap.PodItems)
	m.reconcileBindings(ctx, snap)
	m.publish(m.State())
}
