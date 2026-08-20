package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	helperinstall "github.com/fengqi-dev/kube-loop/internal/helper/install"
)

func (m *Model) beginServiceUninstall() tea.Cmd {
	if m.connected() {
		m.err, m.status = "disconnect before uninstalling the Helper Service", ""
		return nil
	}
	m.err, m.status = "", ""
	m.console.overlay = overlayConfirmServiceUninstall
	return nil
}

func (m Model) uninstallHelperService() tea.Cmd {
	return func() tea.Msg {
		return helperServiceUninstalledMsg{err: helperinstall.Uninstall(m.state.ctx)}
	}
}

type helperServiceUninstalledMsg struct{ err error }
