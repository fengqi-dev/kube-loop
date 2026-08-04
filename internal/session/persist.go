package session

import (
	"context"
	"fmt"

	"github.com/fengqi-dev/kube-loop/internal/intercept"
	"github.com/fengqi-dev/kube-loop/internal/portfwd"
	"github.com/fengqi-dev/kube-loop/internal/store"
)

func WithStore(stateStore *store.Store) Option {
	return func(manager *Manager) { manager.store = stateStore }
}

func (m *Manager) Store() *store.Store { return m.store }

func (m *Manager) RememberSelection(contextName, namespace string) error {
	if m.store == nil {
		return nil
	}
	if err := m.store.SetUI(contextName, namespace); err != nil {
		m.AppendLog("ERROR", fmt.Sprintf(
			"remember cluster selection %s/%s: %v", contextName, namespace, err,
		))
		return err
	}
	return nil
}

func (m *Manager) PreferredSelection() (contextName, namespace string) {
	if m.store == nil {
		return "", ""
	}
	ui := m.store.Snapshot().UI
	return ui.LastContext, ui.LastNamespace
}

func (m *Manager) PreferredConnectionMode(contextName string) ConnectionMode {
	if m.store == nil || contextName == "" {
		return ConnectionModeTUN
	}
	mode := ConnectionMode(m.store.Cluster(contextName).ConnectionMode)
	if mode != ConnectionModeSOCKS {
		return ConnectionModeTUN
	}
	return mode
}

func (m *Manager) clearPersistedSessions() error {
	if m.store == nil {
		return nil
	}
	return m.store.ClearSessionIntents()
}

func (m *Manager) persistPortForwards() {
	if m.store == nil {
		return
	}
	grouped := map[string][]store.PortForwardSpec{}
	for _, item := range m.portfwd.List() {
		grouped[item.Context] = append(grouped[item.Context], store.PortForwardSpec{
			Namespace:  item.Namespace,
			Kind:       item.Kind,
			Name:       item.Name,
			Protocol:   item.Protocol,
			RemotePort: item.RemotePort,
			LocalPort:  item.LocalPort,
		})
	}
	snap := m.store.Snapshot()
	for name, cluster := range snap.Clusters {
		if cluster == nil {
			continue
		}
		if _, ok := grouped[name]; !ok && len(cluster.PortForwards) > 0 {
			grouped[name] = nil
		}
	}
	for contextName, items := range grouped {
		if err := m.store.SetPortForwards(contextName, items); err != nil {
			m.AppendLog("ERROR", fmt.Sprintf("persist port-forwards for %s: %v", contextName, err))
		}
	}
}

func (m *Manager) persistExchanges(contextName string) {
	if m.store == nil || contextName == "" {
		return
	}
	items := m.intercept.List()
	specs := make([]store.ExchangeSpec, 0, len(items))
	for _, item := range items {
		specs = append(specs, store.ExchangeSpec{
			Namespace: item.Namespace,
			Service:   item.Service,
			Ports:     toStorePorts(item.Locals),
		})
	}
	if err := m.store.SetExchanges(contextName, specs); err != nil {
		m.AppendLog("ERROR", fmt.Sprintf("persist exchanges for %s: %v", contextName, err))
	}
}

func (m *Manager) persistMirrors(contextName string) {
	if m.store == nil || contextName == "" {
		return
	}
	items := m.intercept.ListMirrors()
	specs := make([]store.MirrorSpec, 0, len(items))
	for _, item := range items {
		specs = append(specs, store.MirrorSpec{
			Namespace: item.Namespace,
			Service:   item.Service,
			Ports:     toStorePorts(item.Locals),
		})
	}
	if err := m.store.SetMirrors(contextName, specs); err != nil {
		m.AppendLog("ERROR", fmt.Sprintf("persist mirrors for %s: %v", contextName, err))
	}
}

func (m *Manager) persistPreviews(contextName string) {
	if m.store == nil || contextName == "" {
		return
	}
	items := m.intercept.ListPreviews()
	specs := make([]store.PreviewSpec, 0, len(items))
	for _, item := range items {
		specs = append(specs, store.PreviewSpec{
			Namespace: item.Namespace,
			Name:      item.Service,
			Ports:     toStorePorts(item.Locals),
		})
	}
	if err := m.store.SetPreviews(contextName, specs); err != nil {
		m.AppendLog("ERROR", fmt.Sprintf("persist previews for %s: %v", contextName, err))
	}
}

func (m *Manager) PersistShutdown() {
	if m.store == nil {
		return
	}
	m.persistPortForwards()
	state := m.State()
	contextName := state.Context
	namespace := state.Namespace
	if contextName == "" {
		contextName, namespace = m.PreferredSelection()
	}
	connected := state.Phase == PhaseConnected
	if contextName != "" {
		if state.Mode != "" {
			if err := m.store.SetConnectionMode(contextName, string(state.Mode)); err != nil {
				m.AppendLog("ERROR", fmt.Sprintf("persist connection mode: %v", err))
			}
		}
		if connected {
			m.persistExchanges(contextName)
			m.persistMirrors(contextName)
			m.persistPreviews(contextName)
		}
		if err := m.store.SetConnected(contextName, namespace, connected); err != nil {
			m.AppendLog("ERROR", fmt.Sprintf("persist connected flag: %v", err))
		}
	}
}

// RestoreStartup reapplies port-forwards and optionally reconnects the last cluster.
func (m *Manager) RestoreStartup(ctx context.Context) {
	if m.store == nil {
		return
	}
	snap := m.store.Snapshot()
	m.AppendLog("INFO", fmt.Sprintf(
		"startup session restore scan: contexts=%d lastContext=%s",
		len(snap.Clusters), snap.UI.LastContext,
	))
	for contextName, cluster := range snap.Clusters {
		if cluster == nil {
			continue
		}
		if cluster.Connected && contextName == snap.UI.LastContext {
			// Restore these after the selected connection mode is ready:
			// TUN uses its traffic inbound, while SOCKS5 uses the native
			// Kubernetes API port-forward path.
			continue
		}
		for _, item := range cluster.PortForwards {
			info, err := m.portfwd.Start(ctx, portfwd.Request{
				Context:    contextName,
				Namespace:  item.Namespace,
				Kind:       item.Kind,
				Name:       item.Name,
				Protocol:   item.Protocol,
				RemotePort: item.RemotePort,
				LocalPort:  item.LocalPort,
			})
			if err != nil {
				m.AppendLog("ERROR", fmt.Sprintf(
					"restore port-forward %s/%s/%s: %v",
					contextName, item.Kind, item.Name, err,
				))
				continue
			}
			m.AppendLog("INFO", fmt.Sprintf(
				"restored port-forward %s/%s/%s at %s",
				contextName, item.Kind, item.Name, info.Address,
			))
		}
	}

	contextName := snap.UI.LastContext
	cluster := snap.Clusters[contextName]
	if contextName == "" || cluster == nil || !cluster.Connected {
		m.AppendLog("INFO", "startup session restore complete; no cluster reconnect requested")
		return
	}
	namespace := cluster.Namespace
	if namespace == "" {
		namespace = snap.UI.LastNamespace
	}
	if namespace == "" {
		namespace = "default"
	}
	m.AppendLog("INFO", fmt.Sprintf(
		"restoring cluster connection: context=%s namespace=%s",
		contextName, namespace,
	))
	mode := ConnectionMode(cluster.ConnectionMode)
	if mode != ConnectionModeSOCKS {
		mode = ConnectionModeTUN
	}
	if err := m.Connect(ctx, Request{Context: contextName, Namespace: namespace, Mode: mode}); err != nil {
		m.AppendLog("ERROR", fmt.Sprintf("restore connect %s: %v", contextName, err))
	}
}

func (m *Manager) restoreBindings(ctx context.Context, contextName string) {
	if m.store == nil || contextName == "" {
		return
	}
	m.mu.Lock()
	m.restoring = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.restoring = false
		m.mu.Unlock()
	}()

	cluster := m.store.Cluster(contextName)
	total := len(cluster.PortForwards) + len(cluster.Exchanges) +
		len(cluster.Mirrors) + len(cluster.Previews)
	if total == 0 {
		m.AppendLog("INFO", "no persisted sessions to restore for context "+contextName)
		return
	}
	m.AppendLog("INFO", fmt.Sprintf(
		"restoring sessions for %s: portForwards=%d exchanges=%d mirrors=%d previews=%d",
		contextName, len(cluster.PortForwards), len(cluster.Exchanges),
		len(cluster.Mirrors), len(cluster.Previews),
	))
	restored, skipped, failed := m.restorePortForwards(
		ctx, contextName, cluster.PortForwards,
	)
	for _, item := range cluster.Exchanges {
		_, err := m.intercept.StartIntercept(ctx, intercept.Mapping{
			Namespace: item.Namespace,
			Service:   item.Service,
			Ports:     toInterceptPorts(item.Ports),
		})
		if err != nil {
			failed++
			m.AppendLog("ERROR", fmt.Sprintf(
				"restore exchange %s/%s: %v", item.Namespace, item.Service, err,
			))
			continue
		}
		restored++
		m.AppendLog("INFO", fmt.Sprintf(
			"restored exchange %s/%s", item.Namespace, item.Service,
		))
	}
	for _, item := range cluster.Mirrors {
		_, err := m.intercept.StartMirror(ctx, intercept.Mapping{
			Namespace: item.Namespace,
			Service:   item.Service,
			Ports:     toInterceptPorts(item.Ports),
		})
		if err != nil {
			failed++
			m.AppendLog("ERROR", fmt.Sprintf(
				"restore mirror %s/%s: %v", item.Namespace, item.Service, err,
			))
			continue
		}
		restored++
		m.AppendLog("INFO", fmt.Sprintf(
			"restored mirror %s/%s", item.Namespace, item.Service,
		))
	}
	for _, item := range cluster.Previews {
		_, err := m.intercept.StartPreview(ctx, intercept.PreviewRequest{
			Namespace: item.Namespace,
			Name:      item.Name,
			Ports:     toInterceptPorts(item.Ports),
		})
		if err != nil {
			failed++
			m.AppendLog("ERROR", fmt.Sprintf(
				"restore preview %s/%s: %v", item.Namespace, item.Name, err,
			))
			continue
		}
		restored++
		m.AppendLog("INFO", fmt.Sprintf(
			"restored preview %s/%s", item.Namespace, item.Name,
		))
	}
	m.AppendLog("INFO", fmt.Sprintf(
		"session restore complete for %s: restored=%d skipped=%d failed=%d",
		contextName, restored, skipped, failed,
	))
}

func (m *Manager) restoreNativePortForwards(ctx context.Context, contextName string) {
	if m.store == nil || contextName == "" {
		return
	}
	items := m.store.Cluster(contextName).PortForwards
	if len(items) == 0 {
		return
	}
	m.AppendLog("INFO", fmt.Sprintf(
		"restoring native port-forwards for SOCKS5 mode: context=%s count=%d",
		contextName, len(items),
	))
	restored, skipped, failed := m.restorePortForwards(ctx, contextName, items)
	m.AppendLog("INFO", fmt.Sprintf(
		"native port-forward restore complete for %s: restored=%d skipped=%d failed=%d",
		contextName, restored, skipped, failed,
	))
}

func (m *Manager) restorePortForwards(
	ctx context.Context, contextName string, items []store.PortForwardSpec,
) (restored, skipped, failed int) {
	for _, item := range items {
		if m.hasPortForward(contextName, item) {
			skipped++
			continue
		}
		info, err := m.portfwd.Start(ctx, portfwd.Request{
			Context:    contextName,
			Namespace:  item.Namespace,
			Kind:       item.Kind,
			Name:       item.Name,
			Protocol:   item.Protocol,
			RemotePort: item.RemotePort,
			LocalPort:  item.LocalPort,
		})
		if err != nil {
			failed++
			m.AppendLog("ERROR", fmt.Sprintf(
				"restore port-forward %s/%s/%s: %v",
				contextName, item.Kind, item.Name, err,
			))
			continue
		}
		restored++
		m.AppendLog("INFO", fmt.Sprintf(
			"restored port-forward %s/%s/%s at %s",
			contextName, item.Kind, item.Name, info.Address,
		))
	}
	return restored, skipped, failed
}

func (m *Manager) hasPortForward(contextName string, want store.PortForwardSpec) bool {
	wantProtocol := want.Protocol
	if wantProtocol == "" {
		wantProtocol = "tcp"
	}
	for _, item := range m.portfwd.List() {
		if item.Context == contextName &&
			item.Namespace == want.Namespace &&
			item.Kind == want.Kind &&
			item.Name == want.Name &&
			item.Protocol == wantProtocol &&
			item.RemotePort == want.RemotePort &&
			item.LocalPort == want.LocalPort {
			return true
		}
	}
	return false
}

func (m *Manager) isRestoring() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.restoring
}

func (m *Manager) isShuttingDown() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.shuttingDown
}

func toStorePorts(items []intercept.PortMapping) []store.PortMapping {
	out := make([]store.PortMapping, 0, len(items))
	for _, item := range items {
		out = append(out, store.PortMapping{
			ServicePort: item.ServicePort,
			Protocol:    item.Protocol,
			LocalHost:   item.LocalHost,
			LocalPort:   item.LocalPort,
		})
	}
	return out
}

func toInterceptPorts(items []store.PortMapping) []intercept.PortMapping {
	out := make([]intercept.PortMapping, 0, len(items))
	for _, item := range items {
		out = append(out, intercept.PortMapping{
			ServicePort: item.ServicePort,
			Protocol:    item.Protocol,
			LocalHost:   item.LocalHost,
			LocalPort:   item.LocalPort,
		})
	}
	return out
}
