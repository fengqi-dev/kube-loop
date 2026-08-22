package tui

import tea "github.com/charmbracelet/bubbletea"

func (m *Model) updateWorkspaceNavigationKey(key tea.KeyMsg) (tea.Cmd, bool) {
	view := m.workspaceView()
	switch key.String() {
	case keyEsc:
		if view.detail {
			view.detail = false
			m.setWorkspaceView(view)
			return nil, true
		}
		if view.filter != "" {
			view.filter, view.cursor, view.offset = "", 0, 0
			m.setWorkspaceView(view)
			return nil, true
		}
		return m.workspaceHistory(-1), true
	case "-", "[":
		return m.workspaceHistory(-1), true
	case "]":
		return m.workspaceHistory(1), true
	case "q":
		if view.detail || m.workspace.historyPos > 0 {
			if view.detail {
				view.detail = false
				m.setWorkspaceView(view)
				return nil, true
			}
			return m.workspaceHistory(-1), true
		}
		return tea.Quit, true
	case "r":
		m.loading = true
		return tea.Batch(m.spinner.Tick, m.loadWorkspaceData()), true
	case "j", keyDown:
		m.moveWorkspaceCursor(1)
		return nil, true
	case "k", "up":
		m.moveWorkspaceCursor(-1)
		return nil, true
	case "ctrl+d", "pgdown":
		m.moveWorkspaceCursor(m.workspacePageSize())
		return nil, true
	case "ctrl+u", "pgup":
		m.moveWorkspaceCursor(-m.workspacePageSize())
		return nil, true
	case "g", "home":
		m.setWorkspaceCursor(0)
		return nil, true
	case "G", "end":
		m.setWorkspaceCursor(len(m.workspaceFilteredRows()) - 1)
		return nil, true
	case keyEnter:
		resourceHasDetails := m.workspace.resource == resourcePods ||
			m.workspace.resource == resourceServices ||
			m.workspace.resource == resourceTasks
		if resourceHasDetails {
			if len(m.workspaceFilteredRows()) > 0 {
				view.detail = true
				m.setWorkspaceView(view)
			}
			return nil, true
		}
	}
	return m.updateWorkspaceResource(key)
}
