package tui

import tea "github.com/charmbracelet/bubbletea"

func (m *Model) updateConsoleNavigationKey(key tea.KeyMsg) (tea.Cmd, bool) {
	switch key.String() {
	case "?":
		m.console.overlay = overlayHelp
		return nil, true
	case ":":
		m.console.inputMode, m.console.inputText = inputCommand, ""
		m.err, m.status = "", ""
		return nil, true
	case "/":
		if m.mode == viewMain &&
			(m.activeTab == tabWorkloads || m.activeTab == tabServices || m.activeTab == tabTasks) {
			m.console.inputMode, m.console.inputText = inputFilter, m.console.filters[m.activeTab]
			m.err, m.status = "", ""
			return nil, true
		}
	case keyEsc:
		if m.mode == viewMain {
			m.console.filters[m.activeTab] = ""
			m.setConsoleCursor(0)
			m.err, m.status = "", ""
			return nil, true
		}
	case keyTab, keyRight:
		if m.mode == viewMain {
			m.switchConsoleTab(1)
			m.loading = true
			return tea.Batch(m.spinner.Tick, m.loadTabData()), true
		}
	case keyShiftTab, keyLeft:
		if m.mode == viewMain {
			m.switchConsoleTab(-1)
			m.loading = true
			return tea.Batch(m.spinner.Tick, m.loadTabData()), true
		}
	}
	return m.updateConsoleMovementKey(key)
}

func (m *Model) updateConsoleMovementKey(key tea.KeyMsg) (tea.Cmd, bool) {
	switch key.String() {
	case "j", keyDown:
		if m.mode == viewMain && m.activeTab != tabConnection {
			m.moveConsoleSelection(1)
			return nil, true
		}
	case "k", "up":
		if m.mode == viewMain && m.activeTab != tabConnection {
			m.moveConsoleSelection(-1)
			return nil, true
		}
	case "pgdown":
		if m.mode == viewMain && m.activeTab != tabConnection {
			m.moveConsoleSelection(m.consolePageSize())
			return nil, true
		}
	case "pgup":
		if m.mode == viewMain && m.activeTab != tabConnection {
			m.moveConsoleSelection(-m.consolePageSize())
			return nil, true
		}
	case "home", "g":
		if m.mode == viewMain && m.activeTab != tabConnection {
			m.setConsoleCursor(0)
			return nil, true
		}
	case "end", "G":
		if m.mode == viewMain && m.activeTab != tabConnection {
			m.setConsoleCursor(max(0, m.consoleItemCount()-1))
			return nil, true
		}
	}
	return nil, false
}
