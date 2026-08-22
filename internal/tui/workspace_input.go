package tui

import tea "github.com/charmbracelet/bubbletea"

func (m *Model) updateWorkspaceInput(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case keyEsc:
		if m.workspace.input == workspaceInputFilter {
			view := m.workspaceView()
			view.filter, view.cursor, view.offset = m.workspace.inputBefore, 0, 0
			m.setWorkspaceView(view)
		}
		m.workspace.input, m.workspace.inputText = workspaceInputNone, ""
		m.workspace.inputBefore = ""
		m.workspace.commandPos = -1
		return nil
	case keyEnter:
		text, mode := m.workspace.inputText, m.workspace.input
		m.workspace.input, m.workspace.inputText = workspaceInputNone, ""
		m.workspace.inputBefore = ""
		m.workspace.commandPos = -1
		if mode == workspaceInputCommand {
			return m.runWorkspaceCommand(text)
		}
		return nil
	case keyBackspace:
		m.workspace.inputText = trimLastRune(m.workspace.inputText)
	case keyTab, keyShiftTab:
		if m.workspace.input == workspaceInputCommand {
			candidates := m.workspaceCommandCandidates()
			if len(candidates) > 0 {
				delta := 1
				if key.String() == "shift+tab" {
					delta = -1
				}
				m.workspace.suggestion = (m.workspace.suggestion + delta + len(candidates)) % len(candidates)
				m.workspace.inputText = candidates[m.workspace.suggestion]
			}
		}
		return nil
	case "up", keyDown:
		if m.workspace.input == workspaceInputCommand && len(m.workspace.commands) > 0 {
			if m.workspace.commandPos < 0 {
				m.workspace.commandPos = len(m.workspace.commands)
			}
			if key.String() == "up" {
				m.workspace.commandPos = max(0, m.workspace.commandPos-1)
			} else {
				m.workspace.commandPos = minInt(len(m.workspace.commands)-1, m.workspace.commandPos+1)
			}
			m.workspace.inputText = m.workspace.commands[m.workspace.commandPos]
		}
		return nil
	default:
		if key.Type == tea.KeyRunes {
			m.workspace.inputText += string(key.Runes)
		}
	}
	if m.workspace.input == workspaceInputFilter {
		view := m.workspaceView()
		view.filter, view.cursor, view.offset = m.workspace.inputText, 0, 0
		m.setWorkspaceView(view)
		_, err := compileWorkspaceFilter(view.filter)
		if err != nil {
			m.err = err.Error()
		} else {
			m.err = ""
		}
	} else {
		m.workspace.suggestion = 0
	}
	return nil
}
