package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *Model) updateConsoleContextKey(key tea.KeyMsg) (tea.Cmd, bool) {
	switch key.String() {
	case "p":
		if m.mode == viewMain && m.activeTab == tabConnection {
			if m.connected() {
				m.err = errDisconnectBeforeChangingServer
				return nil, true
			}
			m.console.overlay = overlayProfiles
			return loadProfiles(m.state), true
		}
	case "n":
		if m.mode == viewMain && len(m.namespaces) > 0 {
			m.console.overlay = overlayNamespace
			m.console.query, m.console.overlayPos = "", 0
			return nil, true
		}
	case keyEnter, "x", "c":
		if m.mode == viewMain && m.activeTab == tabConnection && m.connected() && m.consoleTaskCount() > 0 {
			m.console.overlay = overlayConfirmDisconnect
			return nil, true
		}
		if m.mode == viewMain && m.activeTab == tabTasks && key.String() == keyEnter && m.consoleItemCount() > 0 {
			if row, ok := m.selectedConsoleTask(); ok {
				m.console.pendingTask = row.index
			}
			m.console.overlay = overlayConfirmTask
			return nil, true
		}
	}
	return nil, false
}

func (m *Model) beginNamespaceSwitch(target string) tea.Cmd {
	if strings.EqualFold(strings.TrimSpace(target), "all") {
		target = ""
	}
	if target == m.namespace {
		m.err, m.status = "", "Namespace unchanged"
		m.namespaceReturnResource = ""
		return nil
	}
	if m.namespaceReturnResource == "" {
		m.namespaceReturnResource = m.workspace.resource
		if m.namespaceReturnResource == resourceConnection || m.namespaceReturnResource == resourceNamespaces {
			m.namespaceReturnResource = resourcePods
		}
	}
	if m.connected() {
		m.pendingNamespace, m.pendingNamespaceSet = target, true
		m.loading = true
		label := target
		if label == "" {
			label = "all namespaces"
		}
		m.err, m.status = "", "Switching to "+label+"..."
		return tea.Batch(m.spinner.Tick, m.disconnectDataPlane())
	}
	if target != "" {
		profile := m.activeProfile
		profile.LastNamespace = target
		_ = m.state.profiles.Upsert(profile)
	}
	return func() tea.Msg { return namespaceChangedMsg{namespace: target, resource: m.namespaceReturnResource} }
}
