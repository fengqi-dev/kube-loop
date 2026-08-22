package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *Model) updateWorkspace(message tea.Msg) (tea.Cmd, bool) {
	m.ensureWorkspace()
	if m.connectionProgressActive() && m.updateConnectionProgressPopup(message) {
		return nil, true
	}
	if m.err != "" && m.workspace.input == workspaceInputNone {
		if cmd, handled := m.updateErrorPopup(message); handled {
			return cmd, true
		}
	}
	if m.console.overlay != overlayNone {
		return m.updateConsole(message)
	}
	if m.loginAdding {
		if key, ok := message.(tea.KeyMsg); ok {
			next, cmd := m.updateAddProfile(key)
			*m = requireModel(next)
			return cmd, true
		}
		if _, ok := message.(tea.MouseMsg); ok {
			return m.updateConsole(message)
		}
		// Async discovery/save results must continue through Model.Update.
		return nil, false
	}
	if _, ok := message.(tea.MouseMsg); ok && m.actionMode != actionNone {
		return m.updateConsole(message)
	}
	if m.actionMode != actionNone {
		return nil, false
	}
	if mouse, ok := message.(tea.MouseMsg); ok {
		return m.updateWorkspaceMouse(tea.MouseEvent(mouse))
	}
	key, ok := message.(tea.KeyMsg)
	if !ok {
		return nil, false
	}
	if key.String() == keyCtrlC {
		return nil, false
	}
	if m.workspace.help {
		if key.String() == "?" || key.String() == keyEsc || key.String() == keyEnter || key.String() == "q" {
			m.workspace.help = false
		}
		return nil, true
	}
	if m.workspace.input != workspaceInputNone {
		return m.updateWorkspaceInput(key), true
	}
	if command, ok := m.workspace.config.Hotkeys[strings.ToLower(key.String())]; ok {
		return m.runWorkspaceCommand(command), true
	}
	if cmd, handled := m.updateWorkspaceModeKey(key); handled {
		return cmd, true
	}
	return m.updateWorkspaceNavigationKey(key)
}
