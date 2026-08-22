package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

func (m *Model) updateConsole(message tea.Msg) (tea.Cmd, bool) {
	if mouse, ok := message.(tea.MouseMsg); ok {
		return m.updateConsoleMouse(tea.MouseEvent(mouse))
	}

	key, ok := message.(tea.KeyMsg)
	if !ok {
		return nil, false
	}
	if key.String() == keyCtrlC {
		return nil, false
	}
	if m.console.overlay != overlayNone {
		return m.updateConsoleOverlay(key), true
	}
	if m.console.inputMode != inputNone {
		return m.updateConsoleInput(key), true
	}
	if m.actionMode != actionNone || m.loginAdding {
		return nil, false
	}
	if cmd, handled := m.updateConsoleNavigationKey(key); handled {
		return cmd, true
	}
	if cmd, handled := m.updateConsoleTaskKey(key); handled {
		return cmd, true
	}
	return m.updateConsoleContextKey(key)
}
