package tui

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
)

const (
	consoleMinWidth  = 60
	consoleMinHeight = 18
	consoleWideWidth = 110
)

type consoleOverlay int

const (
	overlayNone consoleOverlay = iota
	overlayHelp
	overlayNamespace
	overlayConfirmTask
	overlayConfirmProfile
	overlayConfirmDisconnect
	overlayConfirmServiceUninstall
	overlayProfiles
	overlayProfileAdd
)

type consoleFocus int

const (
	focusList consoleFocus = iota
	focusDetail
)

type consoleInput int

const (
	inputNone consoleInput = iota
	inputCommand
	inputFilter
)

const (
	taskFilterAll = iota
	taskFilterForward
	taskFilterTraffic
	taskFilterSSH
	taskFilterExec
	taskFilterCount
)

type consoleViewState struct {
	cursor       int
	offset       int
	detailOffset int
	focus        consoleFocus
}

type consoleState struct {
	views       [tabCount]consoleViewState
	overlay     consoleOverlay
	query       string
	overlayPos  int
	returnTo    consoleOverlay
	taskFilter  int
	pendingTask int
	inputMode   consoleInput
	inputText   string
	filters     [tabCount]string
}

type consoleRow struct {
	title  string
	meta   string
	copy   string
	status string
	detail string
	kind   string
	index  int
}

var (
	consoleInk     = lipgloss.Color("#E8E4D9")
	consoleDim     = lipgloss.Color("#8D98A5")
	consoleCanvas  = lipgloss.Color("#081018")
	consolePanel   = lipgloss.Color("#101C27")
	consoleBorder  = lipgloss.Color("#29404F")
	consoleAmber   = lipgloss.Color("#FFB454")
	consoleTeal    = lipgloss.Color("#4FD6BE")
	consoleDanger  = lipgloss.Color("#FF6B6B")
	consolePrimary = lipgloss.Color("#167D8D")

	consoleTitle     = lipgloss.NewStyle().Foreground(consoleCanvas).Background(consoleAmber).Bold(true).Padding(0, 1)
	consoleSubtle    = lipgloss.NewStyle().Foreground(consoleDim)
	consoleValue     = lipgloss.NewStyle().Foreground(consoleInk)
	consoleSection   = lipgloss.NewStyle().Foreground(consoleAmber).Bold(true)
	consoleNav       = lipgloss.NewStyle().Foreground(consoleDim).Padding(0, 1)
	consoleNavActive = lipgloss.NewStyle().Foreground(consoleCanvas).Background(consoleAmber).Bold(true).Padding(0, 1)
	consoleCard      = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(consoleBorder).
				Background(consolePanel).
				Padding(1, 2)
	consoleDetail     = consoleCard.BorderForeground(consolePrimary)
	consoleOverlayBox = consoleCard.BorderForeground(consoleAmber).Padding(1, 3)
	consoleSelected   = lipgloss.NewStyle().Foreground(consoleCanvas).Background(consoleTeal).Bold(true)
	consoleButton     = lipgloss.NewStyle().
				Foreground(consoleCanvas).
				Background(consolePrimary).
				Bold(true).
				Padding(0, 1)
	consoleDangerButton = consoleButton.Background(consoleDanger)
	consoleError        = lipgloss.NewStyle().Foreground(consoleDanger).Bold(true)
	consoleOK           = lipgloss.NewStyle().Foreground(consoleTeal).Bold(true)
	consoleCmdPrompt    = lipgloss.NewStyle().
				Foreground(consoleCanvas).
				Background(consoleAmber).
				Bold(true).
				Padding(0, 1)
	consoleFilterPrompt = lipgloss.NewStyle().
				Foreground(consoleCanvas).
				Background(consolePrimary).
				Bold(true).
				Padding(0, 1)
	consoleCmdText = lipgloss.NewStyle().Foreground(consoleInk).Bold(true)
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

func (m Model) viewConsole() string {
	if m.width == 0 || m.height == 0 {
		return "Loading KubeLoop..."
	}
	if m.width < consoleMinWidth || m.height < consoleMinHeight {
		return m.viewConsoleMinimum()
	}
	if m.mode == viewLogin {
		return m.viewConsoleProfiles()
	}

	header, footer := m.viewConsoleHeader(), m.viewConsoleFooter()
	bodyHeight := max(8, m.height-lipgloss.Height(header)-lipgloss.Height(footer)-2)
	contentWidth := m.width - 2
	var body string
	if m.width >= consoleWideWidth {
		body = lipgloss.JoinHorizontal(
			lipgloss.Top,
			m.viewConsoleSidebar(bodyHeight),
			" ",
			m.viewConsoleMain(m.width-25, bodyHeight),
		)
	} else {
		nav := m.viewConsoleTopNav()
		body = lipgloss.JoinVertical(
			lipgloss.Left,
			nav,
			m.viewConsoleMain(contentWidth, bodyHeight-lipgloss.Height(nav)-1),
		)
	}
	if m.actionMode != actionNone {
		body = m.viewConsoleAction(contentWidth, bodyHeight)
	}
	if m.console.overlay != overlayNone {
		body = m.viewConsoleOverlay(contentWidth, bodyHeight)
	}
	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

func (m Model) viewConsoleMinimum() string {
	size := fmt.Sprintf(
		"Current: %dx%d  Required: %dx%d",
		m.width,
		m.height,
		consoleMinWidth,
		consoleMinHeight,
	)
	box := consoleOverlayBox.Width(max(30, m.width-4)).Render(
		consoleSection.Render("KUBELOOP OPERATIONS CONSOLE") + "\n\n" +
			"Terminal is too small.\n" + size +
			"\n\nResize the terminal or press q to quit.",
	)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m Model) viewConsoleHeader() string {
	server := firstNonEmpty(m.activeProfile.DisplayName, m.activeProfile.BaseURL, "No server")
	state := consoleSubtle.Render("OFFLINE")
	if m.connected() {
		state = consoleOK.Render("ONLINE")
	}
	left := consoleTitle.Render("KUBELOOP") + "  " + consoleSection.Render("OPERATIONS CONSOLE")
	right := truncateConsole(server, 28) + "  " + state
	gap := strings.Repeat(" ", max(1, m.width-lipgloss.Width(left)-lipgloss.Width(right)-2))
	return lipgloss.NewStyle().Width(m.width).Background(consolePanel).Padding(1, 1).Render(left + gap + right)
}

func (m Model) viewConsoleFooter() string {
	right := "[ list  ] details   y copy   ? help   q quit"
	var left string
	switch m.console.inputMode {
	case inputCommand:
		left = consoleCmdPrompt.Render(":") + consoleCmdText.Render(m.console.inputText+"█")
		right = "Enter run   Esc cancel"
	case inputFilter:
		left = consoleFilterPrompt.Render("/") + consoleCmdText.Render(m.console.inputText+"█")
		right = "Enter keep   Esc clear"
	case inputNone:
		left = m.hintText()
		switch {
		case m.loading:
			left = m.spinner.View() + " Working"
		case m.err != "":
			left = consoleError.Render(truncateConsole(m.err, max(20, m.width-30)))
		case m.status != "":
			left = consoleOK.Render(truncateConsole(m.status, max(20, m.width-30)))
		}
	}
	gap := strings.Repeat(" ", max(1, m.width-lipgloss.Width(left)-lipgloss.Width(right)-2))
	return lipgloss.NewStyle().
		Width(m.width).
		Foreground(consoleDim).
		Background(consolePanel).
		Padding(0, 1).
		Render(left + gap + right)
}

func (m Model) viewConsoleSidebar(height int) string {
	items := make([]string, 0, 2+2*len(tabNames)+5)
	items = append(items, consoleSection.Render("NAVIGATION"), "")
	for i, name := range tabNames {
		style, marker := consoleNav, "  "
		if tab(i) == m.activeTab {
			style, marker = consoleNavActive, "> "
		}
		items = append(items, style.Width(18).Render(marker+name), "")
	}
	items = append(
		items,
		"",
		consoleSubtle.Render("Namespace"),
		truncateConsole(m.namespace, 18),
		"",
		consoleSubtle.Render("Press : for commands"),
	)
	return consoleCard.Width(20).Height(max(8, height-2)).Render(strings.Join(items, "\n"))
}

func (m Model) viewConsoleTopNav() string {
	cell := max(12, (m.width-2)/int(tabCount))
	items := make([]string, 0, int(tabCount))
	for i, name := range tabNames {
		style := consoleNav
		if tab(i) == m.activeTab {
			style = consoleNavActive
		}
		items = append(items, style.Width(cell-2).Align(lipgloss.Center).Render(name))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, items...)
}

func (m Model) viewConsoleMain(width, height int) string {
	switch m.activeTab {
	case tabConnection:
		return m.viewConsoleConnection(width)
	case tabWorkloads:
		return m.viewConsoleRows(
			"WORKLOADS",
			m.consoleWorkloadRows(),
			width,
			height,
			"No workloads found in this namespace.",
		)
	case tabServices:
		return m.viewConsoleRows(
			"SERVICES",
			m.consoleServiceRows(),
			width,
			height,
			"No services found in this namespace.",
		)
	case tabTasks:
		return m.viewConsoleRows(m.consoleTaskTitle(), m.consoleTaskRows(), width, height, "No matching sessions.")
	case tabCount:
		return ""
	}
	return ""
}

func (m Model) viewConsoleConnection(width int) string {
	state := "Disconnected"
	connecting := m.loading && strings.HasPrefix(m.status, "[")
	if connecting {
		state = "Connecting"
	}
	if m.connected() {
		state = consoleStateConnected
	}
	leftFields := renderConsoleField("State", state) +
		renderConsoleField(
			"Server",
			firstNonEmpty(m.activeProfile.DisplayName, m.activeProfile.BaseURL, "Not selected"),
		) +
		renderConsoleField("Namespace", firstNonEmpty(m.namespace, "Not selected")) +
		renderConsoleField("Mode", strings.ToUpper(string(m.selectedMode)))
	left := consoleCard.Width(max(24, width/2-3)).Render(
		consoleSection.Render("CONNECTION") + "\n\n" +
			leftFields,
	)
	right := consoleDetail.Width(max(24, width/2-3)).Render(
		consoleSection.Render("QUICK ACTIONS") +
			"\n\n" + consoleButton.Render(" Enter  Connect / Disconnect ") +
			"\n\n" +
			"m  Switch data-plane mode\n" + "n  Select namespace\n" + ":servers  Manage servers\n" + "L  Log out",
	)
	if width >= 76 {
		return lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
	}
	return lipgloss.JoinVertical(lipgloss.Left, left, right)
}

func (m Model) viewConsoleRows(title string, rows []consoleRow, width, height int, empty string) string {
	if len(rows) == 0 {
		message := empty
		if filter := strings.TrimSpace(m.console.filters[m.activeTab]); filter != "" {
			message = "No matches for /" + filter + ". Press Esc to clear the filter."
		} else if !m.connected() && m.activeTab != tabTasks {
			message = "Connect to a server to load this view."
		}
		content := consoleSection.Render(title) + "\n\n" + m.consoleStateMessage(message)
		return consoleCard.
			Width(max(24, width-4)).
			Height(max(6, height-3)).
			Render(content)
	}
	state := &m.console.views[m.activeTab]
	state.cursor = minInt(len(rows)-1, max(0, state.cursor))
	m.cursor = state.cursor
	listWidth, detailWidth := width, 0
	if width >= 84 {
		listWidth, detailWidth = width*3/5-1, width-(width*3/5-1)-1
	}
	visible := max(3, height-6)
	if state.cursor < state.offset {
		state.offset = state.cursor
	}
	if state.cursor >= state.offset+visible {
		state.offset = state.cursor - visible + 1
	}
	end := minInt(len(rows), state.offset+visible)
	heading := fmt.Sprintf("%s  %d", title, len(rows))
	if filter := strings.TrimSpace(m.console.filters[m.activeTab]); filter != "" {
		heading += consoleSubtle.Render(fmt.Sprintf("  /%s", filter))
	}
	lines := []string{consoleSection.Render(heading), ""}
	for i := state.offset; i < end; i++ {
		row, marker, style := rows[i], "  ", consoleValue
		if i == state.cursor {
			marker, style = "> ", consoleSelected
		}
		line := marker + truncateConsole(row.title, max(12, listWidth-24))
		if row.status != "" {
			line += "  " + truncateConsole(row.status, 12)
		}
		lines = append(lines, style.Width(max(20, listWidth-6)).Render(line))
		if row.meta != "" && listWidth >= 52 {
			lines = append(lines, consoleSubtle.Render("    "+truncateConsole(row.meta, listWidth-10)))
		}
	}
	listStyle := consoleCard
	if state.focus == focusList {
		listStyle = listStyle.BorderForeground(consoleTeal)
	}
	list := listStyle.
		Width(max(24, listWidth-4)).
		Height(max(6, height-3)).
		Render(strings.Join(lines, "\n"))
	if detailWidth == 0 {
		return list
	}
	selected := rows[state.cursor]
	detailStyle := consoleDetail
	if state.focus == focusDetail {
		detailStyle = detailStyle.BorderForeground(consoleTeal)
	}
	detailBody := cropConsoleText(
		firstNonEmpty(selected.detail, "No additional details."),
		state.detailOffset,
		max(3, height-9),
	)
	detailContent := consoleSection.Render("DETAILS") + "\n\n" +
		consoleValue.Render(selected.title) + "\n" +
		consoleSubtle.Render(selected.meta) + "\n\n" +
		lipgloss.NewStyle().Width(max(18, detailWidth-8)).Render(detailBody)
	detail := detailStyle.
		Width(max(22, detailWidth-4)).
		Height(max(6, height-3)).
		Render(detailContent)
	return lipgloss.JoinHorizontal(lipgloss.Top, list, " ", detail)
}

func (m Model) viewConsoleAction(width, height int) string {
	title, description := "PORT FORWARD", "Configure the target, then press Enter to start."
	if m.actionMode == actionExec {
		title, description = "EXECUTE COMMAND", "Type the command to run in the selected pod."
	}
	if m.actionMode == actionExchange || m.actionMode == actionMirror || m.actionMode == actionPreview {
		return m.viewServiceTrafficAction(width, height)
	}
	values := "Target: " + firstNonEmpty(m.actionPod, m.actionService, "-") +
		"\nPort: " + strconv.Itoa(int(m.actionPort)) +
		"\nCommand: " + m.actionCommand
	controls := ""
	if m.actionMode == actionPortForward {
		portName := ""
		if m.actionPortIndex >= 0 && m.actionPortIndex < len(m.actionPorts) &&
			m.actionPorts[m.actionPortIndex].Name != "" {
			portName = m.actionPorts[m.actionPortIndex].Name + "  "
		}
		remoteValue := fmt.Sprintf(
			"%s%d/%s",
			portName,
			m.actionPort,
			strings.ToUpper(firstNonEmpty(m.actionProtocol, "tcp")),
		)
		localValue := firstNonEmpty(m.actionLocalPort, "0")
		if localValue == "0" {
			localValue += " (auto)"
		}
		remotePrefix, localPrefix := "  ", "  "
		if m.actionField == 0 {
			remotePrefix = "> "
		} else {
			localPrefix = "> "
			localValue += "_"
		}
		values = "Target: " + firstNonEmpty(m.actionPod, m.actionService, "-") + "\n\n" +
			remotePrefix + "Remote port  " + remoteValue + "\n" +
			localPrefix + "Local port   " + localValue
		controls = "\n\n" + consoleSubtle.Render("↑/↓ field   ←/→ select port   Tab next   0 = auto")
	}
	content := consoleSection.Render(title) + "\n\n" +
		description + "\n\n" +
		consoleValue.Render(values) + controls + "\n\n" +
		consoleButton.Render(" Enter  Start ") + "  Esc cancel"
	box := consoleOverlayBox.Width(minInt(68, width-8)).Render(content)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

func (m Model) viewConsoleOverlay(width, height int) string {
	var content string
	switch m.console.overlay {
	case overlayHelp:
		const commandHelpText = `
:pods :svc :sessions :conn   Go to view
:ns      Select namespace
:servers Manage servers
:help    This reference
:q       Quit

`
		const keyHelpText = `
/                 Filter current list
Esc               Cancel / clear filter
Tab / Shift+Tab   Change view
j / k             Move or scroll
PgUp / PgDn       Move one page
[ / ]             Focus list / details
Enter             Primary action
n                 Select namespace
t                 Filter sessions
y                 Copy session output
r                 Refresh
?                 Close help
q                 Quit`
		content = consoleSection.Render("KEYBOARD REFERENCE") + "\n\n" +
			consoleSection.Render("Commands (press :)") + commandHelpText +
			consoleSection.Render("Keys") + keyHelpText
	case overlayNamespace:
		matches := m.filteredNamespaces()
		lines := []string{
			consoleSection.Render("SELECT NAMESPACE"),
			"",
			"Search: " + consoleValue.Render(m.console.query+"_"),
			"",
		}
		start, end := max(0, m.console.overlayPos-4), minInt(len(matches), max(0, m.console.overlayPos-4)+9)
		for i := start; i < end; i++ {
			line := "  " + matches[i]
			if i == m.console.overlayPos {
				line = consoleSelected.Width(42).Render("> " + matches[i])
			}
			lines = append(lines, line)
		}
		if len(matches) == 0 {
			lines = append(lines, consoleSubtle.Render("No matching namespaces"))
		}
		lines = append(lines, "", consoleSubtle.Render("Type to filter   Enter select   Esc cancel"))
		content = strings.Join(lines, "\n")
	case overlayConfirmTask:
		content = consoleConfirmContent("STOP SESSION?", "The selected client session will be stopped.", "Stop session")
	case overlayConfirmProfile:
		content = consoleConfirmContent("DELETE SERVER?", "The selected server will be removed.", "Delete server")
	case overlayConfirmDisconnect:
		content = consoleConfirmContent(
			"DISCONNECT?",
			fmt.Sprintf("%d active session(s) will be interrupted.", m.consoleTaskCount()),
			"Disconnect",
		)
	case overlayConfirmServiceUninstall:
		content = consoleConfirmContent(
			"UNINSTALL HELPER SERVICE?",
			"The privileged system Helper Service will be removed.",
			"Uninstall service",
		)
	case overlayProfiles:
		content = m.viewConsoleProfilesOverlay()
	case overlayProfileAdd:
		content = consoleSection.Render("ADD SERVER") + "\n\n" +
			consoleSubtle.Render("Enter the complete HTTP or HTTPS Gateway service address.") + "\n\n" +
			"Service address\n> " + m.loginURL + "_\n\n" +
			"Enter add server   Esc back"
	case overlayNone:
		content = ""
	}
	box := consoleOverlayBox.Width(minInt(62, width-8)).Render(content)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

func (m Model) viewConsoleProfiles() string {
	headerContent := consoleTitle.Render("KUBELOOP") + "  " + consoleSection.Render("SERVERS")
	header := lipgloss.NewStyle().
		Width(m.width).
		Background(consolePanel).
		Padding(1, 1).
		Render(headerContent)
	footerLeft := consoleSubtle.Render("Enter select   a add   d delete   l login   : cmd   ? help")
	if m.console.inputMode == inputCommand {
		footerLeft = consoleCmdPrompt.Render(":") +
			consoleCmdText.Render(m.console.inputText+"█") + "   " +
			consoleSubtle.Render("Enter run   Esc cancel")
	}
	footer := lipgloss.NewStyle().
		Width(m.width).
		Foreground(consoleDim).
		Background(consolePanel).
		Padding(0, 1).
		Render(footerLeft)
	height := max(8, m.height-lipgloss.Height(header)-lipgloss.Height(footer)-2)
	var body string
	if m.loginAdding {
		form := consoleSection.Render("ADD SERVER") + "\n\n" +
			consoleSubtle.Render("Enter the complete HTTP or HTTPS Gateway service address.") + "\n\n" +
			"Service address\n> " + m.loginURL + "_\n\n" +
			"Enter add server   Esc cancel"
		body = lipgloss.Place(
			m.width,
			height,
			lipgloss.Center,
			lipgloss.Center,
			consoleOverlayBox.Width(minInt(72, m.width-8)).Render(form),
		)
	} else {
		lines := []string{consoleSection.Render(fmt.Sprintf("SERVERS  %d", len(m.profiles.Profiles))), ""}
		for i, profile := range m.profiles.Profiles {
			marker, style := "  ", consoleValue
			if i == m.loginCursor {
				marker, style = "> ", consoleSelected
			}
			name := firstNonEmpty(profile.DisplayName, profile.ID, "Unnamed server")
			line := marker + truncateConsole(name, max(18, m.width/2)) + "  " +
				truncateConsole(profile.BaseURL, max(18, m.width/2-8))
			lines = append(lines, style.Width(max(30, m.width-10)).Render(line))
		}
		if len(m.profiles.Profiles) == 0 {
			lines = append(lines, consoleSubtle.Render("No servers. Press a to add one."))
		}
		body = consoleCard.
			Width(m.width - 6).
			Height(max(6, height-3)).
			Render(strings.Join(lines, "\n"))
	}
	if m.console.overlay != overlayNone {
		body = m.viewConsoleOverlay(m.width-2, height)
	}
	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

func (m Model) consoleWorkloadRows() []consoleRow {
	filter := m.console.filters[tabWorkloads]
	rows := make([]consoleRow, 0, len(m.pods))
	for index, pod := range m.pods {
		name := firstNonEmpty(pod.Name, "Unnamed pod")
		phase := firstNonEmpty(pod.Phase, "Unknown")
		node := firstNonEmpty(pod.NodeName, "-")
		totalContainers := pod.TotalContainers
		if totalContainers == 0 {
			totalContainers = containerCount(pod.Containers)
		}
		readyContainers := pod.ReadyContainers
		if readyContainers == 0 && pod.Ready {
			readyContainers = totalContainers
		}
		ready := fmt.Sprintf("%d/%d", readyContainers, totalContainers)
		containers := firstNonEmpty(strings.Join(pod.Containers, ", "), "-")
		ports := formatPodPorts(pod.Ports)
		meta := fmt.Sprintf(
			"node %s   ready %s   restarts %d   age %s",
			node,
			ready,
			pod.Restarts,
			formatResourceAge(pod.AgeSeconds),
		)
		detail := "Namespace: " + firstNonEmpty(pod.Namespace, m.namespace) +
			"\nPhase: " + phase +
			"\nPod IP: " + firstNonEmpty(pod.PodIP, "-") +
			"\nNode: " + node +
			"\nReady: " + ready +
			fmt.Sprintf("\nRestarts: %d\nAge: %s", pod.Restarts, formatResourceAge(pod.AgeSeconds)) +
			"\nContainers: " + containers +
			"\nPorts: " + ports +
			"\n\nEnter/f port forward   s SSH   e exec"
		row := consoleRow{title: name, status: phase, meta: meta, index: index, detail: detail}
		if consoleRowMatchesFilter(row, filter) {
			rows = append(rows, row)
		}
	}
	return rows
}

func containerCount(containers []string) int32 {
	if len(containers) > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(len(containers)) //nolint:gosec // Length is checked against MaxInt32 above.
}

func (m Model) consoleServiceRows() []consoleRow {
	filter := m.console.filters[tabServices]
	rows := make([]consoleRow, 0, len(m.services))
	for index, service := range m.services {
		name := firstNonEmpty(service.Name, "Unnamed service")
		typeName := firstNonEmpty(service.Type, "Service")
		ports := firstNonEmpty(formatServicePorts(service.Ports), "-")
		ip := firstNonEmpty(service.ClusterIP, "-")
		externalIP := firstNonEmpty(strings.Join(service.ExternalIPs, ","), "<none>")
		detail := "Namespace: " + firstNonEmpty(service.Namespace, m.namespace) +
			"\nType: " + typeName +
			"\nCluster IP: " + ip +
			"\nExternal IP: " + externalIP +
			"\nAge: " + formatResourceAge(service.AgeSeconds) +
			"\nPorts: " + ports +
			"\n\nEnter/f start port forward"
		row := consoleRow{
			title: name, status: typeName, meta: "ports " + ports, index: index, detail: detail,
		}
		if consoleRowMatchesFilter(row, filter) {
			rows = append(rows, row)
		}
	}
	return rows
}

func (m Model) consoleTaskRows() []consoleRow {
	filter := m.console.filters[tabTasks]
	rows := make([]consoleRow, 0, m.consoleTaskCount())
	for index, task := range m.portForwards {
		if m.console.taskFilter != taskFilterAll && m.console.taskFilter != taskFilterForward {
			continue
		}
		detail := fmt.Sprintf(
			"State: %s\nNamespace: %s\nKind: %s\nProtocol: %s\nLocal: %s\nRemote: %s:%d",
			task.State,
			task.Namespace,
			task.Kind,
			task.Protocol,
			task.Address,
			task.DialAddress,
			task.RemotePort,
		)
		row := consoleRow{
			title:  task.Name,
			status: "FORWARD",
			kind:   taskKindForward,
			index:  index,
			meta:   task.Address,
			detail: detail,
		}
		if consoleRowMatchesFilter(row, filter) {
			rows = append(rows, row)
		}
	}
	base := len(m.portForwards)
	for index, task := range m.exchanges {
		if m.console.taskFilter == taskFilterAll || m.console.taskFilter == taskFilterTraffic {
			row := trafficConsoleRow(
				"EXCHANGE",
				"exchange",
				base+index,
				task.Service,
				task.Namespace,
				task.ClusterIP,
				task.State,
				task.Targets,
			)
			if consoleRowMatchesFilter(row, filter) {
				rows = append(rows, row)
			}
		}
	}
	base += len(m.exchanges)
	for index, task := range m.mirrors {
		if m.console.taskFilter == taskFilterAll || m.console.taskFilter == taskFilterTraffic {
			row := mirrorConsoleRow(base+index, task)
			if consoleRowMatchesFilter(row, filter) {
				rows = append(rows, row)
			}
		}
	}
	base += len(m.mirrors)
	for index, task := range m.previews {
		if m.console.taskFilter == taskFilterAll || m.console.taskFilter == taskFilterTraffic {
			row := trafficConsoleRow(
				"PREVIEW",
				taskKindPreview,
				base+index,
				task.Name,
				task.Namespace,
				task.ClusterIP,
				task.State,
				task.Targets,
			)
			if consoleRowMatchesFilter(row, filter) {
				rows = append(rows, row)
			}
		}
	}
	base += len(m.previews)
	for index, task := range m.podSSHEndpoints {
		if m.console.taskFilter != taskFilterAll && m.console.taskFilter != taskFilterSSH {
			continue
		}
		detail := "State: " + task.State +
			"\nNamespace: " + task.Namespace +
			"\nPod IP: " + task.PodIP +
			"\nContainer: " + task.Container +
			"\nAddress: " + task.Address +
			"\nCommand: " + task.Command
		row := consoleRow{
			title: task.Pod, status: taskStatusSSH, kind: taskKindSSH, index: base + index,
			meta: task.Command, detail: detail,
		}
		if consoleRowMatchesFilter(row, filter) {
			rows = append(rows, row)
		}
	}
	for index, task := range m.execTasks {
		if m.console.taskFilter != taskFilterAll && m.console.taskFilter != taskFilterExec {
			continue
		}
		detail := "Command: " + task.Command + "\nState: " + task.State
		if task.Output != "" {
			detail += "\n\nOUTPUT\n" + task.Output
		}
		row := consoleRow{
			title: task.Pod, status: taskStatusExec, kind: taskKindExec,
			index: base + len(m.podSSHEndpoints) + index, meta: task.Command, detail: detail,
		}
		if consoleRowMatchesFilter(row, filter) {
			rows = append(rows, row)
		}
	}
	return rows
}

func (m Model) consoleItemCount() int {
	switch m.activeTab {
	case tabWorkloads:
		return len(m.consoleWorkloadRows())
	case tabServices:
		return len(m.consoleServiceRows())
	case tabTasks:
		return len(m.consoleTaskRows())
	case tabConnection, tabCount:
		return 0
	}
	return 0
}

func (m Model) consoleTaskCount() int {
	return len(m.portForwards) + len(m.exchanges) + len(m.mirrors) +
		len(m.previews) + len(m.podSSHEndpoints) + len(m.execTasks)
}

func (m Model) consoleExecIndex(rowIndex int) int {
	return rowIndex - len(m.portForwards) - len(m.exchanges) - len(m.mirrors) -
		len(m.previews) - len(m.podSSHEndpoints)
}

func (m Model) filteredNamespaces() []string {
	query := strings.ToLower(strings.TrimSpace(m.console.query))
	matches := make([]string, 0, len(m.namespaces)+1)
	//nolint:gocritic // The user query is intentionally matched against the literal namespace candidate.
	if query == "" || strings.Contains("all", query) {
		matches = append(matches, "all")
	}
	for _, namespace := range m.namespaces {
		if query == "" || strings.Contains(strings.ToLower(namespace.Name), query) {
			matches = append(matches, namespace.Name)
		}
	}
	return matches
}

func (m Model) consoleTaskTitle() string {
	labels := []string{"ALL", "FORWARD", "TRAFFIC", taskStatusSSH, taskStatusExec}
	return "SESSIONS · " + labels[minInt(m.console.taskFilter, len(labels)-1)]
}

func (m Model) selectedConsoleTask() (consoleRow, bool) {
	rows := m.consoleTaskRows()
	if m.cursor < 0 || m.cursor >= len(rows) {
		return consoleRow{}, false
	}
	return rows[m.cursor], true
}

func (m *Model) clearCompletedExecTasks() {
	kept := m.execTasks[:0]
	removed := 0
	for _, task := range m.execTasks {
		if task.State == taskStateRunning {
			kept = append(kept, task)
		} else {
			removed++
		}
	}
	m.execTasks = kept
	m.status = fmt.Sprintf("Cleared %d completed exec session(s)", removed)
	m.setConsoleCursor(minInt(m.cursor, max(0, m.consoleItemCount()-1)))
}

func (m *Model) copySelectedTaskOutput() tea.Cmd {
	row, ok := m.selectedConsoleTask()
	if !ok {
		m.err = "select a session to copy"
		return nil
	}
	value := firstNonEmpty(row.copy, row.meta)
	if row.kind == taskKindExec {
		index := m.consoleExecIndex(row.index)
		if index >= 0 && index < len(m.execTasks) && m.execTasks[index].Output != "" {
			value = m.execTasks[index].Output
		}
	}
	if strings.TrimSpace(value) == "" {
		value = row.detail
	}
	if strings.TrimSpace(value) == "" {
		m.err = "selected session has no copyable details"
		return nil
	}
	m.err, m.status = "", "Copying "+strings.ToUpper(row.kind)+" session..."
	return copySessionToClipboard(m.context(), row.kind, value)
}

func (m Model) consoleStateMessage(empty string) string {
	if m.loading {
		return m.spinner.View() + " Loading..."
	}
	if m.err != "" {
		return consoleError.Render(m.err) + "\n\n" + consoleSubtle.Render("Press r to retry")
	}
	return consoleSubtle.Render(empty)
}

func (m Model) viewConsoleProfilesOverlay() string {
	lines := []string{consoleSection.Render("MANAGE SERVERS"), ""}
	for index, profile := range m.profiles.Profiles {
		marker, style := "  ", consoleValue
		if index == m.loginCursor {
			marker, style = "> ", consoleSelected
		}
		name := truncateConsole(firstNonEmpty(profile.DisplayName, profile.ID), 20)
		line := marker + name + "  " + truncateConsole(profile.BaseURL, 24)
		lines = append(lines, style.Width(48).Render(line))
	}
	if len(m.profiles.Profiles) == 0 {
		lines = append(lines, consoleSubtle.Render("No servers configured."))
	}
	lines = append(lines, "", consoleSubtle.Render("Enter select   l login   a add   d delete   Esc close"))
	return strings.Join(lines, "\n")
}

func formatPodPorts(ports []clientremote.PodPort) string {
	items := make([]string, 0, len(ports))
	for _, port := range ports {
		name := ""
		if port.Name != "" {
			name = port.Name + ":"
		}
		items = append(items, fmt.Sprintf("%s%d/%s", name, port.Port, port.Protocol))
	}
	return firstNonEmpty(strings.Join(items, ", "), "-")
}

func cropConsoleText(value string, offset, height int) string {
	lines := strings.Split(value, "\n")
	offset = minInt(max(0, offset), max(0, len(lines)-1))
	return strings.Join(lines[offset:minInt(len(lines), offset+height)], "\n")
}

func consoleConfirmContent(title, message, action string) string {
	return consoleError.Render(title) + "\n\n" + message + "\n\n" +
		consoleDangerButton.Render(" Enter / y  "+action) + "  " +
		consoleSubtle.Render("n / Esc cancel")
}

func renderConsoleField(label, value string) string {
	return consoleSubtle.Width(14).Render(label) + consoleValue.Render(value) + "\n\n"
}

func truncateConsole(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(value) <= width {
		return value
	}
	if width <= 3 {
		return strings.Repeat(".", width)
	}
	runes := []rune(value)
	for len(runes) > 0 && lipgloss.Width(string(runes))+3 > width {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "..."
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" && value != "<nil>" {
			return value
		}
	}
	return ""
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
