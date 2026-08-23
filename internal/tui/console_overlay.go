package tui

import (
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

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
