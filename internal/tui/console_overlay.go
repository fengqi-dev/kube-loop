package tui

import (
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

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
	if m.actionMode != actionNone {
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
			if !m.loginAdding {
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
	if m.loginAdding {
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
		m.loginCursor = row
		return nil, true
	}
	return nil, false
}

func (m *Model) updateConsoleProfileOverlayMouse(event tea.MouseEvent) tea.Cmd {
	row := event.Y - (m.height/2 - len(m.profiles.Profiles)/2)
	if row >= 0 && row < len(m.profiles.Profiles) {
		m.loginCursor = row
	}
	return nil
}

func (m *Model) updateConsoleOverlay(key tea.KeyMsg) tea.Cmd {
	switch m.console.overlay {
	case overlayHelp:
		if key.String() == "?" || key.String() == keyEsc || key.String() == keyEnter || key.String() == "q" {
			m.closeConsoleOverlay()
		}
	case overlayNamespace:
		return m.updateNamespaceOverlay(key)
	case overlayProfiles:
		return m.updateProfilesOverlay(key)
	case overlayProfileAdd:
		next, cmd := m.updateAddProfile(key)
		*m = requireModel(next)
		if !m.loginAdding {
			m.console.overlay = overlayProfiles
		}
		return cmd
	case overlayConfirmTask, overlayConfirmProfile, overlayConfirmDisconnect, overlayConfirmServiceUninstall:
		return m.updateConfirmationOverlay(key)
	case overlayNone:
		return nil
	}
	return nil
}

func (m *Model) updateNamespaceOverlay(key tea.KeyMsg) tea.Cmd {
	matches := m.filteredNamespaces()
	switch key.String() {
	case keyEsc:
		m.closeConsoleOverlay()
	case "up", "ctrl+k":
		m.console.overlayPos = max(0, m.console.overlayPos-1)
	case keyDown, "ctrl+j":
		m.console.overlayPos = minInt(max(0, len(matches)-1), m.console.overlayPos+1)
	case keyBackspace:
		if len(m.console.query) > 0 {
			_, size := utf8.DecodeLastRuneInString(m.console.query)
			m.console.query = m.console.query[:len(m.console.query)-size]
			m.console.overlayPos = 0
		}
	case keyEnter:
		if len(matches) == 0 {
			return nil
		}
		target := matches[minInt(m.console.overlayPos, len(matches)-1)]
		m.closeConsoleOverlay()
		return m.beginNamespaceSwitch(target)
	default:
		if key.Type == tea.KeyRunes {
			m.console.query += string(key.Runes)
			m.console.overlayPos = 0
		}
	}
	return nil
}

func (m *Model) updateProfilesOverlay(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case keyEsc, "p":
		m.closeConsoleOverlay()
	case "up", "k":
		m.loginCursor = max(0, m.loginCursor-1)
	case keyDown, "j":
		m.loginCursor = minInt(max(0, len(m.profiles.Profiles)-1), m.loginCursor+1)
	case "a", "n":
		m.loginAdding, m.loginURL = true, ""
		m.console.overlay = overlayProfileAdd
	case "d":
		if len(m.profiles.Profiles) > 0 {
			m.console.returnTo = overlayProfiles
			m.console.overlay = overlayConfirmProfile
		}
	case keyEnter:
		next, cmd := m.updateLogin(key)
		*m = requireModel(next)
		return cmd
	case "l", "L":
		m.closeConsoleOverlay()
		next, cmd := m.updateLogin(key)
		*m = requireModel(next)
		return cmd
	}
	return nil
}

func (m *Model) updateConfirmationOverlay(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case keyEsc, "n":
		m.cancelConsoleOverlay()
		return nil
	case keyEnter, "y":
		return m.confirmConsoleOverlay()
	default:
		return nil
	}
}

func (m *Model) confirmConsoleOverlay() tea.Cmd {
	action := m.console.overlay
	returnTo := m.console.returnTo
	m.closeConsoleOverlay()
	switch action {
	case overlayConfirmTask:
		m.loading, m.status = true, "Stopping session..."
		return tea.Batch(m.spinner.Tick, m.stopTaskAt(m.console.pendingTask))
	case overlayConfirmProfile:
		next, cmd := m.updateLogin(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
		*m = requireModel(next)
		m.console.overlay = returnTo
		return cmd
	case overlayConfirmDisconnect:
		next, cmd := m.updateOverview(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
		*m = requireModel(next)
		return cmd
	case overlayConfirmServiceUninstall:
		m.loading, m.status = true, "Uninstalling Helper Service..."
		return tea.Batch(m.spinner.Tick, m.uninstallHelperService())
	case overlayNone, overlayHelp, overlayNamespace, overlayProfiles, overlayProfileAdd:
		return nil
	}
	return nil
}

func (m *Model) closeConsoleOverlay() {
	m.console.overlay = overlayNone
	m.console.returnTo = overlayNone
	m.console.query, m.console.overlayPos = "", 0
}

func (m *Model) cancelConsoleOverlay() {
	if m.console.returnTo != overlayNone {
		m.console.overlay = m.console.returnTo
		m.console.returnTo = overlayNone
		return
	}
	m.closeConsoleOverlay()
}

// updateConsoleInput handles typing while the command (:) or filter (/) bar is open.
