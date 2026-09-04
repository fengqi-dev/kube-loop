package tui

import tea "github.com/charmbracelet/bubbletea"

func (m *Model) updateConsoleMouse(event tea.MouseEvent) (tea.Cmd, bool) {
	if event.Button == tea.MouseButtonWheelUp || event.Button == tea.MouseButtonWheelDown {
		delta := -1
		if event.Button == tea.MouseButtonWheelDown {
			delta = 1
		}
		if m.console.overlay == overlayNamespace {
			m.console.overlayPos = minInt(max(0, len(m.filteredNamespaces())-1), max(0, m.console.overlayPos+delta))
			return nil, true
		}
		if m.mode == viewMain && m.activeTab != tabConnection {
			m.moveConsoleSelection(delta)
			return nil, true
		}
		return nil, false
	}
	if event.Action != tea.MouseActionPress || event.Button != tea.MouseButtonLeft {
		return nil, false
	}
	if m.console.overlay != overlayNone {
		return m.updateConsoleOverlayMouse(event), true
	}
	if m.action.mode != actionNone {
		return m.updateConsoleActionMouse(event), true
	}
	if m.mode == viewLogin {
		return m.updateConsoleProfileMouse(event)
	}

	if m.width >= consoleWideWidth && event.X < 22 && event.Y >= 4 && event.Y < 4+int(tabCount)*2 {
		next := (event.Y - 4) / 2
		if next >= 0 && next < int(tabCount) {
			m.selectConsoleTab(tab(next))
			m.loading = true
			return tea.Batch(m.spinner.Tick, m.loadTabData()), true
		}
	}
	if m.width < consoleWideWidth && event.Y == 3 {
		cell := max(1, m.width/int(tabCount))
		m.selectConsoleTab(tab(minInt(int(tabCount)-1, event.X/cell)))
		m.loading = true
		return tea.Batch(m.spinner.Tick, m.loadTabData()), true
	}
	if m.activeTab == tabConnection {
		if event.Y >= 6 && event.Y <= 9 && event.X >= m.width/2 {
			key := tea.KeyMsg{Type: tea.KeyEnter}
			if cmd, handled := m.updateConsole(key); handled {
				return cmd, true
			}
			next, cmd := m.updateOverview(key)
			*m = requireModel(next)
			return cmd, true
		}
		return nil, false
	}
	if m.width >= 84 && event.X >= m.consoleDetailStartX() {
		m.console.views[m.activeTab].focus = focusDetail
		return nil, true
	}
	rowY, stride := m.consoleListGeometry()
	if event.Y >= rowY {
		clicked := (event.Y - rowY) / stride
		state := &m.console.views[m.activeTab]
		index := state.offset + clicked
		if index >= 0 && index < m.consoleItemCount() {
			state.cursor, state.focus, m.cursor = index, focusList, index
			return nil, true
		}
	}
	return nil, false
}

func (m *Model) updateConsoleOverlayMouse(event tea.MouseEvent) tea.Cmd {
	switch m.console.overlay {
	case overlayConfirmTask, overlayConfirmProfile, overlayConfirmDisconnect, overlayConfirmServiceUninstall:
		if event.Y >= m.height/2+1 {
			key := keyEnter
			if event.X >= m.width/2 {
				key = "n"
			}
			return m.updateConsoleOverlay(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		}
	case overlayHelp:
		m.closeConsoleOverlay()
	case overlayNamespace:
		matches := m.filteredNamespaces()
		start := max(0, m.console.overlayPos-4)
		row := event.Y - (m.height/2 - 4)
		if row >= 0 && start+row < len(matches) {
			m.console.overlayPos = start + row
		}
	case overlayProfiles:
		return m.updateConsoleProfileOverlayMouse(event)
	case overlayProfileAdd:
		if event.Y > m.height/2+1 {
			key := tea.KeyMsg{Type: tea.KeyEnter}
			if event.X >= m.width/2+10 {
				key = tea.KeyMsg{Type: tea.KeyEsc}
			}
			next, cmd := m.updateAddProfile(key)
			*m = requireModel(next)
			if !m.login.adding {
				m.console.overlay = overlayProfiles
			}
			return cmd
		}
	case overlayNone:
		return nil
	}
	return nil
}

func (m *Model) updateConsoleActionMouse(event tea.MouseEvent) tea.Cmd {
	if event.Y < m.height/2+2 {
		return nil
	}
	key := tea.KeyMsg{Type: tea.KeyEnter}
	if event.X >= m.width/2+12 {
		key = tea.KeyMsg{Type: tea.KeyEsc}
	}
	next, cmd := m.updateAction(key)
	*m = requireModel(next)
	return cmd
}

func (m *Model) updateConsoleProfileMouse(event tea.MouseEvent) (tea.Cmd, bool) {
	if m.login.adding {
		if event.Y < m.height/2+2 {
			return nil, true
		}
		key := tea.KeyMsg{Type: tea.KeyEnter}
		if event.X >= m.width/2+12 {
			key = tea.KeyMsg{Type: tea.KeyEsc}
		}
		next, cmd := m.updateLogin(key)
		*m = requireModel(next)
		return cmd, true
	}
	row := event.Y - 6
	if row >= 0 && row < len(m.profiles.Profiles) {
		m.login.cursor = row
		return nil, true
	}
	return nil, false
}

func (m *Model) updateConsoleProfileOverlayMouse(event tea.MouseEvent) tea.Cmd {
	row := event.Y - (m.height/2 - len(m.profiles.Profiles)/2)
	if row >= 0 && row < len(m.profiles.Profiles) {
		m.login.cursor = row
	}
	return nil
}
