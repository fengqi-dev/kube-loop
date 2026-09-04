package tui

import tea "github.com/charmbracelet/bubbletea"

func (m *Model) updateConsoleTaskKey(key tea.KeyMsg) (tea.Cmd, bool) {
	switch key.String() {
	case "[":
		m.console.views[m.activeTab].focus = focusList
		return nil, true
	case "]":
		if m.activeTab != tabConnection && m.width >= 84 {
			m.console.views[m.activeTab].focus = focusDetail
			return nil, true
		}
	case "C":
		if m.mode == viewMain && m.activeTab == tabTasks {
			m.clearCompletedExecTasks()
			return nil, true
		}
	case "t":
		if m.mode == viewMain && m.activeTab == tabTasks {
			m.console.taskFilter = (m.console.taskFilter + 1) % taskFilterCount
			m.setConsoleCursor(0)
			return nil, true
		}
	case "e":
		if m.mode == viewMain && m.activeTab == tabTasks {
			if row, ok := m.selectedConsoleTask(); ok && row.kind == taskKindExec {
				index := m.consoleExecIndex(row.index)
				if index >= 0 && index < len(m.execTasks) {
					task := m.execTasks[index]
					m.action.mode, m.action.pod, m.action.command = actionExec, task.Pod, task.Command
					return nil, true
				}
			}
		}
	case "y":
		if m.mode == viewMain && m.activeTab == tabTasks {
			return m.copySelectedTaskOutput(), true
		}
	case "d":
		if m.mode == viewMain && m.activeTab == tabTasks && m.consoleItemCount() > 0 {
			if row, ok := m.selectedConsoleTask(); ok {
				m.console.pendingTask = row.index
			}
			m.console.overlay = overlayConfirmTask
			return nil, true
		}
		if m.mode == viewLogin && len(m.profiles.Profiles) > 0 {
			m.console.returnTo = overlayNone
			m.console.overlay = overlayConfirmProfile
			return nil, true
		}
	}
	return nil, false
}
