package tui

import tea "github.com/charmbracelet/bubbletea"

func (m *Model) updateWorkspaceModeKey(key tea.KeyMsg) (tea.Cmd, bool) {
	view := m.workspaceView()
	switch key.String() {
	case "0":
		m.namespaceReturnResource = m.workspace.resource
		return m.beginNamespaceSwitch("all"), true
	case "1":
		target := m.activeProfile.LastNamespace
		if !containsNamespace(m.namespaces, target) && len(m.namespaces) > 0 {
			target = m.namespaces[0].Name
		}
		if target == "" {
			m.err = "no namespace available"
			return nil, true
		}
		m.namespaceReturnResource = m.workspace.resource
		return m.beginNamespaceSwitch(target), true
	case ":":
		m.workspace.input, m.workspace.inputText = workspaceInputCommand, ""
		m.workspace.suggestion, m.workspace.commandPos = 0, -1
		m.err, m.status = "", ""
		return nil, true
	case "/":
		if m.workspace.resource == resourceConnection {
			m.err = ""
			m.status = "Filter is available on resource lists; press : for commands"
			return nil, true
		}
		m.workspace.input, m.workspace.inputText, m.workspace.inputBefore = workspaceInputFilter, view.filter, view.filter
		m.err, m.status = "", ""
		return nil, true
	case "n":
		resourceSupportsNamespaces := m.workspace.resource == resourcePods ||
			m.workspace.resource == resourceServices
		if m.mode == viewMain && resourceSupportsNamespaces && len(m.namespaces) > 0 {
			m.console.overlay = overlayNamespace
			m.console.query, m.console.overlayPos = "", 0
			m.err, m.status = "", ""
			return nil, true
		}
	case "?":
		m.workspace.help = true
		return nil, true
	}
	return nil, false
}
