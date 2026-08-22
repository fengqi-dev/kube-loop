package tui

import tea "github.com/charmbracelet/bubbletea"

func appendUniqueWorkspaceCommand(commands []string, command string) []string {
	if len(commands) > 0 && commands[len(commands)-1] == command {
		return commands
	}
	commands = append(commands, command)
	if len(commands) > 100 {
		commands = append([]string(nil), commands[len(commands)-100:]...)
	}
	return commands
}

func (m *Model) workspaceNavigate(resource workspaceResource, record bool) tea.Cmd {
	if resource != resourceProfiles && m.mode == viewLogin && !m.authSession.Authenticated {
		m.err = "log in to open " + string(resource)
		return nil
	}
	if resource == resourceProfiles {
		if m.connected() {
			m.err = errDisconnectBeforeChangingServer
			return nil
		}
		if !m.authSession.Authenticated {
			m.mode = viewLogin
		} else {
			m.mode = viewMain
		}
	} else {
		m.mode = viewMain
	}
	descriptor := workspaceDescriptor(resource)
	if descriptor.hasTab {
		m.activeTab = descriptor.legacyTab
	}
	m.workspace.resource = resource
	if record {
		if m.workspace.historyPos+1 < len(m.workspace.history) {
			m.workspace.history = append([]workspaceResource(nil), m.workspace.history[:m.workspace.historyPos+1]...)
		}
		if len(m.workspace.history) == 0 || m.workspace.history[len(m.workspace.history)-1] != resource {
			m.workspace.history = append(m.workspace.history, resource)
		}
		m.workspace.historyPos = len(m.workspace.history) - 1
	}
	m.cursor, m.err, m.status, m.loading = 0, "", "", true
	return tea.Batch(m.spinner.Tick, m.beginWorkspaceLoad())
}

func (m *Model) workspaceHistory(delta int) tea.Cmd {
	if len(m.workspace.history) == 0 {
		return nil
	}
	next := minInt(len(m.workspace.history)-1, max(0, m.workspace.historyPos+delta))
	if next == m.workspace.historyPos {
		return nil
	}
	m.workspace.historyPos = next
	return m.workspaceNavigate(m.workspace.history[next], false)
}
