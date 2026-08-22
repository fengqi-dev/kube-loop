package tui

import tea "github.com/charmbracelet/bubbletea"

func (m *Model) beginConnect() tea.Cmd {
	if m.connected() {
		m.err, m.status = "", "Already connected"
		return nil
	}
	next, cmd := m.updateOverview(tea.KeyMsg{Type: tea.KeyEnter})
	*m = requireModel(next)
	return cmd
}

func (m *Model) beginDisconnect() tea.Cmd {
	if !m.connected() {
		m.err, m.status = "", "Already disconnected"
		return nil
	}
	if m.consoleTaskCount() > 0 {
		m.console.overlay = overlayConfirmDisconnect
		return nil
	}
	next, cmd := m.updateOverview(tea.KeyMsg{Type: tea.KeyEnter})
	*m = requireModel(next)
	return cmd
}
