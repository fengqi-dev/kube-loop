package tui

import (
	"strings"

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
					m.actionMode, m.actionPod, m.actionCommand = actionExec, task.Pod, task.Command
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

func (m *Model) updateConsoleContextKey(key tea.KeyMsg) (tea.Cmd, bool) {
	switch key.String() {
	case "p":
		if m.mode == viewMain && m.activeTab == tabConnection {
			if m.connected() {
				m.err = errDisconnectBeforeChangingServer
				return nil, true
			}
			m.console.overlay = overlayProfiles
			return loadProfiles(m.state), true
		}
	case "n":
		if m.mode == viewMain && len(m.namespaces) > 0 {
			m.console.overlay = overlayNamespace
			m.console.query, m.console.overlayPos = "", 0
			return nil, true
		}
	case keyEnter, "x", "c":
		if m.mode == viewMain && m.activeTab == tabConnection && m.connected() && m.consoleTaskCount() > 0 {
			m.console.overlay = overlayConfirmDisconnect
			return nil, true
		}
		if m.mode == viewMain && m.activeTab == tabTasks && key.String() == keyEnter && m.consoleItemCount() > 0 {
			if row, ok := m.selectedConsoleTask(); ok {
				m.console.pendingTask = row.index
			}
			m.console.overlay = overlayConfirmTask
			return nil, true
		}
	}
	return nil, false
}

func (m *Model) beginNamespaceSwitch(target string) tea.Cmd {
	if strings.EqualFold(strings.TrimSpace(target), "all") {
		target = ""
	}
	if target == m.namespace {
		m.err, m.status = "", "Namespace unchanged"
		m.namespaceReturnResource = ""
		return nil
	}
	if m.namespaceReturnResource == "" {
		m.namespaceReturnResource = m.workspace.resource
		if m.namespaceReturnResource == resourceConnection || m.namespaceReturnResource == resourceNamespaces {
			m.namespaceReturnResource = resourcePods
		}
	}
	if m.connected() {
		m.pendingNamespace, m.pendingNamespaceSet = target, true
		m.loading = true
		label := target
		if label == "" {
			label = "all namespaces"
		}
		m.err, m.status = "", "Switching to "+label+"..."
		return tea.Batch(m.spinner.Tick, m.disconnectDataPlane())
	}
	if target != "" {
		profile := m.activeProfile
		profile.LastNamespace = target
		_ = m.state.profiles.Upsert(profile)
	}
	return func() tea.Msg { return namespaceChangedMsg{namespace: target, resource: m.namespaceReturnResource} }
}

func (m *Model) updateConsoleInput(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case keyEsc:
		if m.console.inputMode == inputFilter {
			m.console.filters[m.activeTab] = ""
			m.setConsoleCursor(0)
		}
		m.console.inputMode, m.console.inputText = inputNone, ""
		return nil
	case keyEnter:
		text, mode := m.console.inputText, m.console.inputMode
		m.console.inputMode, m.console.inputText = inputNone, ""
		if mode == inputCommand {
			return m.runConsoleCommand(text)
		}
		return nil
	case keyBackspace:
		m.console.inputText = trimLastRune(m.console.inputText)
	case keyLeft, keyRight, "up", keyDown, keyTab, keyShiftTab:
		return nil
	default:
		if key.Type == tea.KeyRunes {
			m.console.inputText += string(key.Runes)
		}
	}
	if m.console.inputMode == inputFilter {
		m.console.filters[m.activeTab] = m.console.inputText
		m.setConsoleCursor(0)
	}
	return nil
}

// runConsoleCommand resolves a k9s-style ":" command.
func (m *Model) runConsoleCommand(command string) tea.Cmd {
	command = strings.ToLower(strings.TrimSpace(command))
	switch command {
	case "":
		return nil
	case "q", "quit", "exit":
		return tea.Quit
	case "h", commandHelp, "?":
		m.console.overlay = overlayHelp
		return nil
	case "ns", commandNamespace, "namespaces":
		if m.mode != viewMain {
			m.err = "log in to select a namespace"
			return nil
		}
		if len(m.namespaces) == 0 {
			m.err = "no namespaces loaded"
			return nil
		}
		m.console.overlay, m.console.query, m.console.overlayPos = overlayNamespace, "", 0
		return nil
	case "server", string(resourceProfiles):
		if m.mode != viewMain {
			m.err = "already managing servers"
			return nil
		}
		if m.connected() {
			m.err = "disconnect before changing server"
			return nil
		}
		m.console.overlay = overlayProfiles
		return loadProfiles(m.state)
	case "c", commandConnection, "connection":
		return m.gotoConsoleTab(tabConnection)
	case "w", "po", resourceKindPod, string(resourcePods), "workloads":
		return m.gotoConsoleTab(tabWorkloads)
	case "v", commandService, resourceKindService, string(resourceServices):
		return m.gotoConsoleTab(tabServices)
	case "s", "session", string(resourceTasks), "fw", taskKindForward, "forwards":
		return m.gotoConsoleTab(tabTasks)
	}
	m.err = "unknown command: " + command
	return nil
}

func (m *Model) gotoConsoleTab(next tab) tea.Cmd {
	if m.mode != viewMain || !m.authSession.Authenticated {
		m.err = "log in to open " + tabNames[next]
		return nil
	}
	if next == m.activeTab {
		return nil
	}
	m.selectConsoleTab(next)
	m.loading = true
	return tea.Batch(m.spinner.Tick, m.loadTabData())
}

func consoleRowMatchesFilter(row consoleRow, filter string) bool {
	filter = strings.ToLower(strings.TrimSpace(filter))
	if filter == "" {
		return true
	}
	return strings.Contains(strings.ToLower(row.title), filter) ||
		strings.Contains(strings.ToLower(row.meta), filter) ||
		strings.Contains(strings.ToLower(row.status), filter)
}

// selectedConsoleRow returns the filtered row under the cursor for the active tab.
func (m Model) selectedConsoleRow() (consoleRow, bool) {
	var rows []consoleRow
	switch m.activeTab {
	case tabWorkloads:
		rows = m.consoleWorkloadRows()
	case tabServices:
		rows = m.consoleServiceRows()
	case tabTasks:
		rows = m.consoleTaskRows()
	case tabConnection, tabCount:
		return consoleRow{}, false
	}
	if m.cursor < 0 || m.cursor >= len(rows) {
		return consoleRow{}, false
	}
	return rows[m.cursor], true
}

func (m *Model) switchConsoleTab(delta int) {
	next := (int(m.activeTab) + delta + int(tabCount)) % int(tabCount)
	m.selectConsoleTab(tab(next))
}

func (m *Model) selectConsoleTab(next tab) {
	m.console.views[m.activeTab].cursor = m.cursor
	m.activeTab = next
	m.cursor = m.console.views[next].cursor
	m.err, m.status = "", ""
}

func (m *Model) moveConsoleSelection(delta int) {
	state := &m.console.views[m.activeTab]
	if state.focus == focusDetail {
		state.detailOffset = max(0, state.detailOffset+delta)
		return
	}
	m.setConsoleCursor(state.cursor + delta)
}

func (m *Model) setConsoleCursor(cursor int) {
	total := m.consoleItemCount()
	state := &m.console.views[m.activeTab]
	if total == 0 {
		state.cursor, state.offset, m.cursor = 0, 0, 0
		return
	}
	state.cursor = minInt(total-1, max(0, cursor))
	page := m.consolePageSize()
	if state.cursor < state.offset {
		state.offset = state.cursor
	}
	if state.cursor >= state.offset+page {
		state.offset = state.cursor - page + 1
	}
	state.detailOffset = 0
	m.cursor = state.cursor
}

func (m Model) consolePageSize() int { return max(3, m.height-12) }

func (m Model) consoleListGeometry() (int, int) {
	rowY := 7
	if m.width < consoleWideWidth {
		rowY = 8
	}
	stride := 1
	contentWidth := m.width - 2
	if m.width >= consoleWideWidth {
		contentWidth = m.width - 25
	}
	listWidth := contentWidth
	if contentWidth >= 84 {
		listWidth = contentWidth*3/5 - 1
	}
	if listWidth >= 52 {
		stride = 2
	}
	return rowY, stride
}

func (m Model) consoleDetailStartX() int {
	contentStart, contentWidth := 0, m.width-2
	if m.width >= consoleWideWidth {
		contentStart, contentWidth = 23, m.width-25
	}
	if contentWidth < 84 {
		return m.width
	}
	return contentStart + contentWidth*3/5
}
