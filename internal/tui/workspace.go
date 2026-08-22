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

func (m *Model) updateWorkspaceModeKey(key tea.KeyMsg) (tea.Cmd, bool) {
	view := m.workspaceView()
	switch key.String() {
	case "0":
		m.namespaceReturnResource = m.workspace.resource
		return m.beginNamespaceSwitch("all"), true
	case "1":
		target := m.activeProfile.LastNamespace
		if !containsNamespace(m.namespaces, target) && len(m.namespaces) > 0 {
			target = m.namespaces[0].Name
		}
		if target == "" {
			m.err = "no namespace available"
			return nil, true
		}
		m.namespaceReturnResource = m.workspace.resource
		return m.beginNamespaceSwitch(target), true
	case ":":
		m.workspace.input, m.workspace.inputText = workspaceInputCommand, ""
		m.workspace.suggestion, m.workspace.commandPos = 0, -1
		m.err, m.status = "", ""
		return nil, true
	case "/":
		if m.workspace.resource == resourceConnection {
			m.err = ""
			m.status = "Filter is available on resource lists; press : for commands"
			return nil, true
		}
		m.workspace.input, m.workspace.inputText, m.workspace.inputBefore = workspaceInputFilter, view.filter, view.filter
		m.err, m.status = "", ""
		return nil, true
	case "n":
		resourceSupportsNamespaces := m.workspace.resource == resourcePods ||
			m.workspace.resource == resourceServices
		if m.mode == viewMain && resourceSupportsNamespaces && len(m.namespaces) > 0 {
			m.console.overlay = overlayNamespace
			m.console.query, m.console.overlayPos = "", 0
			m.err, m.status = "", ""
			return nil, true
		}
	case "?":
		m.workspace.help = true
		return nil, true
	}
	return nil, false
}

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

func (m *Model) runWorkspaceCommand(command string) tea.Cmd {
	command = strings.TrimSpace(strings.TrimPrefix(command, ":"))
	if command == "" {
		return nil
	}
	m.workspace.commands = appendUniqueWorkspaceCommand(m.workspace.commands, command)
	fields := strings.Fields(strings.ToLower(command))
	name := fields[0]
	switch name {
	case "connect":
		return m.beginConnect()
	case "disconnect":
		return m.beginDisconnect()
	case "logout":
		return m.beginLogout()
	case "uninstall-service", "uninstall-helper":
		return m.beginServiceUninstall()
	}
	switch name {
	case "q", "quit", "exit":
		return tea.Quit
	case "h", commandHelp, "?":
		m.workspace.help = true
		return nil
	}
	resource, ok := resolveWorkspaceResource(name, m.workspace.config)
	if !ok {
		m.err = "unknown command: " + command
		return nil
	}
	if resource == resourcePods || resource == resourceServices {
		if len(fields) > 1 {
			target := fields[1]
			if target == "all" {
				target = ""
			}
			if target != "" && !containsNamespace(m.namespaces, target) {
				m.err = "unknown namespace: " + fields[1]
				return nil
			}
			if target == m.namespace {
				return m.workspaceNavigate(resource, true)
			}
			m.namespaceReturnResource = resource
			return m.beginNamespaceSwitch(target)
		}
	}
	return m.workspaceNavigate(resource, true)
}

func (m *Model) beginConnect() tea.Cmd {
	if m.connected() {
		m.err, m.status = "", "Already connected"
		return nil
	}
	next, cmd := m.updateOverview(tea.KeyMsg{Type: tea.KeyEnter})
	*m = requireModel(next)
	return cmd
}

func (m *Model) beginDisconnect() tea.Cmd {
	if !m.connected() {
		m.err, m.status = "", "Already disconnected"
		return nil
	}
	if m.consoleTaskCount() > 0 {
		m.console.overlay = overlayConfirmDisconnect
		return nil
	}
	next, cmd := m.updateOverview(tea.KeyMsg{Type: tea.KeyEnter})
	*m = requireModel(next)
	return cmd
}

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
