package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	clientdataplane "github.com/fengqi-dev/kube-loop/internal/client/dataplane"
)

func (m Model) updateOverview(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyEnter, "c", "x":
		if m.activeProfile.ID == "" || !m.authSession.Authenticated {
			m.err = "not authenticated"
			return m, nil
		}
		m.loading, m.err = true, ""
		if m.connected() {
			m.status = "Disconnecting..."
			return m, tea.Batch(m.spinner.Tick, m.disconnectDataPlane())
		}
		m.beginConnectionProgress()
		return m, tea.Batch(m.spinner.Tick, m.connectDataPlane())
	case "m":
		previous := m.selectedMode
		if previous == clientdataplane.ModeTUN {
			m.selectedMode = clientdataplane.ModeSOCKS
		} else {
			m.selectedMode = clientdataplane.ModeTUN
		}
		m.err = ""
		if m.connected() {
			m.loading = true
			m.status = fmt.Sprintf("Switching to %s...", m.selectedMode)
			return m, tea.Batch(m.spinner.Tick, m.switchDataPlaneMode(previous))
		}
		return m, nil
	case "n":
		return m, m.cycleNamespace()
	case "p":
		if m.connected() {
			m.err = errDisconnectBeforeChangingServer
			return m, nil
		}
		m.mode, m.err = viewLogin, ""
		return m, loadProfiles(m.state)
	case "L":
		if m.connected() {
			m.err = errDisconnectBeforeLogout
			return m, nil
		}
		m.loading, m.mode = true, viewLogin
		return m, tea.Batch(m.spinner.Tick, m.logout())
	}
	return m, nil
}

func (m *Model) cycleNamespace() tea.Cmd {
	if len(m.namespaces) == 0 {
		m.err = "no namespaces loaded"
		return nil
	}
	next := m.namespaces[0].Name
	for i, namespace := range m.namespaces {
		if namespace.Name == m.namespace {
			next = m.namespaces[(i+1)%len(m.namespaces)].Name
			break
		}
	}
	return m.beginNamespaceSwitch(next)
}
