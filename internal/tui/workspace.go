package tui

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	workspaceMinWidth  = 60
	workspaceMinHeight = 18
)

type workspaceResource string

const (
	resourceConnection workspaceResource = "connection"
	resourcePods       workspaceResource = "pods"
	resourceServices   workspaceResource = "services"
	resourceTasks      workspaceResource = "sessions"
	resourceProfiles   workspaceResource = "servers"
	resourceNamespaces workspaceResource = "namespaces"
)

type workspaceResourceDescriptor struct {
	id        workspaceResource
	title     string
	aliases   []string
	legacyTab tab
	hasTab    bool
	actions   string
}

var workspaceResourceRegistry = []workspaceResourceDescriptor{
	{id: resourceConnection, title: "Connection", aliases: []string{"c", "conn"}, legacyTab: tabConnection, hasTab: true, actions: "a add server  c connect  enter toggle  m mode  u uninstall service  L logout"},
	{id: resourcePods, title: "Pods", aliases: []string{"w", "workload", "workloads", "po", "pod"}, legacyTab: tabWorkloads, hasTab: true, actions: "enter inspect  n namespace  f forward  s ssh"},
	{id: resourceServices, title: "Services", aliases: []string{"v", "svc", "service"}, legacyTab: tabServices, hasTab: true, actions: "enter inspect  n namespace  f forward  x exchange  m mirror  p preview"},
	{id: resourceTasks, title: "Sessions", aliases: []string{"s", "session", "fw", "forward", "forwards"}, legacyTab: tabTasks, hasTab: true, actions: "enter inspect  d stop  e rerun  y copy  C clear"},
	{id: resourceProfiles, title: "Servers", aliases: []string{"server"}, actions: "enter select  a add  l login  L logout  d delete"},
	{id: resourceNamespaces, title: "Namespaces", aliases: []string{"n", "ns", "namespace"}, actions: "enter select"},
}

type workspaceInput int

const (
	workspaceInputNone workspaceInput = iota
	workspaceInputCommand
	workspaceInputFilter
)

type workspaceViewState struct {
	cursor int
	offset int
	filter string
	detail bool
}

type workspaceState struct {
	initialized    bool
	resource       workspaceResource
	loadGeneration uint64
	views          map[workspaceResource]workspaceViewState
	input          workspaceInput
	inputText      string
	inputBefore    string
	suggestion     int
	commands       []string
	commandPos     int
	history        []workspaceResource
	historyPos     int
	help           bool
	config         workspaceConfig
	warning        string
}

func newWorkspaceState(configPath string) workspaceState {
	config, warning := loadWorkspaceConfig(configPath)
	return workspaceState{
		initialized: true,
		resource:    resourceConnection,
		views:       map[workspaceResource]workspaceViewState{},
		commandPos:  -1,
		history:     []workspaceResource{resourceConnection},
		config:      config,
		warning:     warning,
	}
}

func builtinWorkspaceResource(value string) (workspaceResource, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, descriptor := range workspaceResourceRegistry {
		if value == string(descriptor.id) {
			return descriptor.id, true
		}
		if slices.Contains(descriptor.aliases, value) {
			return descriptor.id, true
		}
	}
	return "", false
}

func resolveWorkspaceResource(value string, config workspaceConfig) (workspaceResource, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	if target, ok := config.Aliases[value]; ok {
		value = target
	}
	return builtinWorkspaceResource(value)
}

func workspaceDescriptor(id workspaceResource) workspaceResourceDescriptor {
	for _, descriptor := range workspaceResourceRegistry {
		if descriptor.id == id {
			return descriptor
		}
	}
	return workspaceResourceRegistry[0]
}

func (m *Model) ensureWorkspace() {
	if m.workspace.initialized {
		return
	}
	m.workspace = newWorkspaceState(m.state.configPath)
	if m.mode == viewLogin {
		m.workspace.resource = resourceProfiles
		m.workspace.history = []workspaceResource{resourceProfiles}
	}
}

func (m *Model) updateWorkspace(message tea.Msg) (tea.Cmd, bool) {
	m.ensureWorkspace()
	if m.connectionProgressActive() {
		if cmd, handled := m.updateConnectionProgressPopup(message); handled {
			return cmd, true
		}
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
			*m = next.(Model)
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
	if key.String() == "ctrl+c" {
		return nil, false
	}
	if m.workspace.help {
		if key.String() == "?" || key.String() == "esc" || key.String() == "enter" || key.String() == "q" {
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
		if m.mode == viewMain && (m.workspace.resource == resourcePods || m.workspace.resource == resourceServices) && len(m.namespaces) > 0 {
			m.console.overlay = overlayNamespace
			m.console.query, m.console.overlayPos = "", 0
			m.err, m.status = "", ""
			return nil, true
		}
	case "?":
		m.workspace.help = true
		return nil, true
	case "esc":
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
	case "j", "down":
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
	case "enter":
		if m.workspace.resource == resourcePods || m.workspace.resource == resourceServices || m.workspace.resource == resourceTasks {
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
	case "esc":
		if m.workspace.input == workspaceInputFilter {
			view := m.workspaceView()
			view.filter, view.cursor, view.offset = m.workspace.inputBefore, 0, 0
			m.setWorkspaceView(view)
		}
		m.workspace.input, m.workspace.inputText = workspaceInputNone, ""
		m.workspace.inputBefore = ""
		m.workspace.commandPos = -1
		return nil
	case "enter":
		text, mode := m.workspace.inputText, m.workspace.input
		m.workspace.input, m.workspace.inputText = workspaceInputNone, ""
		m.workspace.inputBefore = ""
		m.workspace.commandPos = -1
		if mode == workspaceInputCommand {
			return m.runWorkspaceCommand(text)
		}
		return nil
	case "backspace":
		m.workspace.inputText = trimLastRune(m.workspace.inputText)
	case "tab", "shift+tab":
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
	case "up", "down":
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
	case "h", "help", "?":
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
	*m = next.(Model)
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
	*m = next.(Model)
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
			m.err = "disconnect before changing server"
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

func (m Model) loadWorkspaceData() tea.Cmd {
	switch m.workspace.resource {
	case resourceProfiles:
		return loadProfiles(m.state)
	case resourceNamespaces:
		return loadNamespaces(m)
	default:
		return m.loadTabData()
	}
}

type workspaceLoadedMsg struct {
	resource   workspaceResource
	generation uint64
	message    tea.Msg
}

func (m *Model) beginWorkspaceLoad() tea.Cmd {
	m.workspace.loadGeneration++
	resource, generation := m.workspace.resource, m.workspace.loadGeneration
	wrap := func(cmd tea.Cmd) tea.Cmd {
		if cmd == nil {
			return nil
		}
		return func() tea.Msg {
			return workspaceLoadedMsg{resource: resource, generation: generation, message: cmd()}
		}
	}
	var commands []tea.Cmd
	switch resource {
	case resourceProfiles:
		commands = []tea.Cmd{wrap(loadProfiles(m.state))}
	case resourceNamespaces:
		commands = []tea.Cmd{wrap(loadNamespaces(*m))}
	case resourceConnection:
		commands = []tea.Cmd{wrap(loadAuthStatus(*m)), wrap(loadDataPlaneStatus(*m)), wrap(loadNamespaces(*m))}
	case resourcePods:
		commands = []tea.Cmd{wrap(loadNamespaces(*m))}
		commands = append(commands, wrap(loadPods(*m)))
	case resourceServices:
		commands = []tea.Cmd{wrap(loadNamespaces(*m))}
		commands = append(commands, wrap(loadServices(*m)))
	case resourceTasks:
		commands = []tea.Cmd{wrap(loadPortForwards(m.state, m.activeProfile.ID)), wrap(loadTrafficOperations(m.state, m.activeProfile.ID)), wrap(loadPodSSH(m.state, m.activeProfile.ID))}
	}
	return tea.Batch(commands...)
}

func (m *Model) updateWorkspaceResource(key tea.KeyMsg) (tea.Cmd, bool) {
	rows := m.workspaceFilteredRows()
	view := m.workspaceView()
	selected := consoleRow{}
	if view.cursor >= 0 && view.cursor < len(rows) {
		selected = rows[view.cursor]
	}
	switch m.workspace.resource {
	case resourceConnection:
		switch key.String() {
		case "a":
			if m.connected() {
				m.err, m.status = "disconnect before adding a server", ""
				return nil, true
			}
			m.loginAdding = true
			m.loginURL = ""
			m.err, m.status = "", ""
			return m.workspaceNavigate(resourceProfiles, true), true
		case "p":
			return m.workspaceNavigate(resourcePods, true), true
		case "v":
			return m.workspaceNavigate(resourceServices, true), true
		case "n":
			return m.workspaceNavigate(resourceNamespaces, true), true
		case "s":
			return m.workspaceNavigate(resourceTasks, true), true
		case "L":
			return m.beginLogout(), true
		case "u":
			return m.beginServiceUninstall(), true
		case "enter", "c", "x":
			if m.connected() && m.consoleTaskCount() > 0 {
				m.console.overlay = overlayConfirmDisconnect
				return nil, true
			}
		}
		next, cmd := m.updateOverview(key)
		*m = next.(Model)
		return cmd, true
	case resourcePods:
		if len(rows) == 0 {
			return nil, true
		}
		m.activeTab = tabWorkloads
		m.cursor = selected.index
		m.console.filters[tabWorkloads] = ""
		next, cmd := m.updatePods(key)
		*m = next.(Model)
		return cmd, true
	case resourceServices:
		if key.String() == "p" {
			next, cmd := m.updateServices(key)
			*m = next.(Model)
			return cmd, true
		}
		if len(rows) == 0 {
			return nil, true
		}
		m.activeTab = tabServices
		m.cursor = selected.index
		m.console.filters[tabServices] = ""
		next, cmd := m.updateServices(key)
		*m = next.(Model)
		return cmd, true
	case resourceTasks:
		m.activeTab = tabTasks
		position := m.workspaceLegacyTaskPosition(selected)
		if position >= 0 {
			m.cursor = position
		}
		m.console.filters[tabTasks] = ""
		if cmd, handled := m.updateConsole(key); handled {
			return cmd, true
		}
		return nil, true
	case resourceProfiles:
		if key.String() == "d" && len(m.profiles.Profiles) > 0 {
			m.loginCursor = view.cursor
			m.console.returnTo = overlayNone
			m.console.overlay = overlayConfirmProfile
			return nil, true
		}
		m.loginCursor = view.cursor
		next, cmd := m.updateLogin(key)
		*m = next.(Model)
		view.cursor = m.loginCursor
		m.setWorkspaceView(view)
		return cmd, true
	case resourceNamespaces:
		if key.String() == "enter" && selected.title != "" {
			m.namespaceReturnResource = resourcePods
			return m.beginNamespaceSwitch(selected.title), true
		}
	}
	return nil, true
}

func (m Model) workspaceLegacyTaskPosition(selected consoleRow) int {
	copy := m
	copy.console.filters[tabTasks] = ""
	for position, row := range copy.consoleTaskRows() {
		if row.kind == selected.kind && row.index == selected.index {
			return position
		}
	}
	return -1
}

func (m *Model) updateWorkspaceMouse(event tea.MouseEvent) (tea.Cmd, bool) {
	if event.X >= m.width {
		return nil, false
	}
	if event.Action != tea.MouseActionPress {
		return nil, false
	}
	if event.Button == tea.MouseButtonWheelUp || event.Button == tea.MouseButtonWheelDown {
		delta := -1
		if event.Button == tea.MouseButtonWheelDown {
			delta = 1
		}
		m.moveWorkspaceCursor(delta)
		return nil, true
	}
	if event.Button != tea.MouseButtonLeft {
		return nil, false
	}
	if event.Y >= 5 && event.Y < m.height-2 {
		view := m.workspaceView()
		index := view.offset + event.Y - 7
		if index >= 0 && index < len(m.workspaceFilteredRows()) {
			view.cursor = index
			m.setWorkspaceView(view)
			return nil, true
		}
	}
	return nil, false
}

func (m Model) workspaceRawRows() []consoleRow {
	copy := m
	for i := range copy.console.filters {
		copy.console.filters[i] = ""
	}
	switch m.workspace.resource {
	case resourcePods:
		return copy.consoleWorkloadRows()
	case resourceServices:
		return copy.consoleServiceRows()
	case resourceTasks:
		return copy.consoleTaskRows()
	case resourceProfiles:
		rows := make([]consoleRow, 0, len(m.profiles.Profiles))
		for index, profile := range m.profiles.Profiles {
			status := "Ready"
			if profile.ID == m.profiles.ActiveProfileID {
				status = "Active"
			}
			rows = append(rows, consoleRow{title: firstNonEmpty(profile.DisplayName, profile.ID), meta: profile.BaseURL, status: status, kind: "profile", index: index, detail: "Endpoint: " + profile.BaseURL + "\nTunnel path: " + profile.TunnelPath + "\nLast namespace: " + firstNonEmpty(profile.LastNamespace, "-")})
		}
		return rows
	case resourceNamespaces:
		rows := make([]consoleRow, 0, len(m.namespaces))
		for index, namespace := range m.namespaces {
			status := ""
			if namespace.Name == m.namespace {
				status = "Active"
			}
			rows = append(rows, consoleRow{title: namespace.Name, status: status, kind: "namespace", index: index, detail: "Namespace: " + namespace.Name})
		}
		return rows
	}
	return nil
}

type compiledWorkspaceFilter struct {
	regex   *regexp.Regexp
	fuzzy   string
	inverse bool
}

func compileWorkspaceFilter(raw string) (compiledWorkspaceFilter, error) {
	filter := strings.TrimSpace(raw)
	compiled := compiledWorkspaceFilter{}
	if strings.HasPrefix(filter, "!") {
		compiled.inverse = true
		filter = strings.TrimSpace(strings.TrimPrefix(filter, "!"))
	}
	if after, ok := strings.CutPrefix(filter, "-f"); ok {
		compiled.fuzzy = strings.ToLower(strings.TrimSpace(after))
		return compiled, nil
	}
	if filter == "" {
		return compiled, nil
	}
	regex, err := regexp.Compile("(?i)" + filter)
	if err != nil {
		return compiled, fmt.Errorf("invalid filter: %w", err)
	}
	compiled.regex = regex
	return compiled, nil
}

func (m Model) workspaceFilteredRows() []consoleRow {
	rows := m.workspaceRawRows()
	filter, err := compileWorkspaceFilter(m.workspaceView().filter)
	if err != nil {
		return rows
	}
	if filter.regex == nil && filter.fuzzy == "" {
		return rows
	}
	filtered := make([]consoleRow, 0, len(rows))
	for _, row := range rows {
		value := strings.ToLower(row.title + " " + row.meta + " " + row.status)
		matched := filter.regex != nil && filter.regex.MatchString(value)
		if filter.fuzzy != "" {
			matched = workspaceFuzzyMatch(row.title, filter.fuzzy)
		}
		if filter.inverse {
			matched = !matched
		}
		if matched {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func workspaceFuzzyMatch(value, query string) bool {
	queryRunes := []rune(strings.ToLower(query))
	if len(queryRunes) == 0 {
		return true
	}
	position := 0
	for _, valueRune := range []rune(strings.ToLower(value)) {
		if valueRune == queryRunes[position] {
			position++
			if position == len(queryRunes) {
				return true
			}
		}
	}
	return false
}

func (m Model) workspaceView() workspaceViewState {
	view := m.workspace.views[m.workspace.resource]
	rows := m.workspaceFilteredRowsWithoutViewRecursion(view.filter)
	if len(rows) == 0 {
		view.cursor, view.offset = 0, 0
	} else {
		view.cursor = minInt(len(rows)-1, max(0, view.cursor))
	}
	return view
}

func (m Model) workspaceFilteredRowsWithoutViewRecursion(filterText string) []consoleRow {
	copy := m
	view := copy.workspace.views[copy.workspace.resource]
	view.filter = filterText
	copy.workspace.views[copy.workspace.resource] = view
	rows := copy.workspaceRawRows()
	filter, err := compileWorkspaceFilter(filterText)
	if err != nil || (filter.regex == nil && filter.fuzzy == "") {
		return rows
	}
	filtered := make([]consoleRow, 0, len(rows))
	for _, row := range rows {
		value := strings.ToLower(row.title + " " + row.meta + " " + row.status)
		matched := filter.regex != nil && filter.regex.MatchString(value)
		if filter.fuzzy != "" {
			matched = workspaceFuzzyMatch(value, filter.fuzzy)
		}
		if filter.inverse {
			matched = !matched
		}
		if matched {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func (m *Model) setWorkspaceView(view workspaceViewState) {
	if m.workspace.views == nil {
		m.workspace.views = map[workspaceResource]workspaceViewState{}
	}
	m.workspace.views[m.workspace.resource] = view
}

func (m *Model) moveWorkspaceCursor(delta int) {
	m.setWorkspaceCursor(m.workspaceView().cursor + delta)
}

func (m *Model) setWorkspaceCursor(cursor int) {
	rows := m.workspaceFilteredRows()
	view := m.workspaceView()
	if len(rows) == 0 {
		view.cursor, view.offset = 0, 0
		m.setWorkspaceView(view)
		return
	}
	view.cursor = minInt(len(rows)-1, max(0, cursor))
	page := m.workspacePageSize()
	if view.cursor < view.offset {
		view.offset = view.cursor
	}
	if view.cursor >= view.offset+page {
		view.offset = view.cursor - page + 1
	}
	m.setWorkspaceView(view)
}

func (m Model) workspacePageSize() int { return max(3, m.height-10) }

func (m Model) workspaceCommandCandidates() []string {
	prefix := strings.ToLower(strings.TrimSpace(m.workspace.inputText))
	set := map[string]struct{}{"connect": {}, "disconnect": {}, "help": {}, "logout": {}, "q": {}, "uninstall-service": {}}
	for _, descriptor := range workspaceResourceRegistry {
		set[string(descriptor.id)] = struct{}{}
		for _, alias := range descriptor.aliases {
			set[alias] = struct{}{}
		}
	}
	for alias := range m.workspace.config.Aliases {
		set[alias] = struct{}{}
	}
	candidates := make([]string, 0, len(set))
	for candidate := range set {
		if prefix == "" || strings.HasPrefix(candidate, prefix) {
			candidates = append(candidates, candidate)
		}
	}
	sort.Strings(candidates)
	return candidates
}

func (m Model) viewWorkspace() string {
	if !m.workspace.initialized {
		return m.viewConsole()
	}
	if m.width < workspaceMinWidth || m.height < workspaceMinHeight {
		return m.viewWorkspaceTooSmall()
	}
	if m.workspace.help && m.err == "" && !m.connectionProgressActive() {
		return m.viewWorkspaceHelp()
	}
	header := m.viewWorkspaceHeader()
	commandBar := m.viewWorkspaceCommandBar()
	top := header
	if commandBar != "" {
		top = lipgloss.JoinVertical(lipgloss.Left, header, commandBar)
	}
	footer := m.viewWorkspaceFooter()
	bodyHeight := max(5, m.height-lipgloss.Height(top)-lipgloss.Height(footer))
	if m.err != "" && m.workspace.input == workspaceInputNone {
		body := m.viewErrorPopup(bodyHeight)
		return lipgloss.JoinVertical(lipgloss.Left, top, body, footer)
	}
	if m.connectionProgressActive() {
		body := m.viewConnectionProgressPopup(bodyHeight)
		return lipgloss.JoinVertical(lipgloss.Left, top, body, footer)
	}
	if m.loginAdding {
		progress := ""
		if m.loading {
			progress = "\n\n" + m.spinner.View() + " Discovering server…"
		}
		form := consoleSection.Render("ADD SERVER") + "\n\n" +
			consoleSubtle.Render("Enter the complete HTTP or HTTPS Gateway service address.") + "\n\n" +
			"Service address\n> " + m.loginURL + "_\n\n" +
			"Enter add server   Esc cancel" + progress
		body := lipgloss.Place(m.width, bodyHeight, lipgloss.Center, lipgloss.Center, consoleOverlayBox.Copy().Width(minInt(72, m.width-8)).Render(form))
		return lipgloss.JoinVertical(lipgloss.Left, top, body, footer)
	}
	if m.actionMode != actionNone {
		body := m.viewConsoleAction(m.width, bodyHeight)
		return lipgloss.JoinVertical(lipgloss.Left, top, body, footer)
	}
	if m.console.overlay != overlayNone {
		body := m.viewConsoleOverlay(m.width, bodyHeight)
		return lipgloss.JoinVertical(lipgloss.Left, top, body, footer)
	}
	body := m.viewWorkspaceBody(bodyHeight)
	return lipgloss.JoinVertical(lipgloss.Left, top, body, footer)
}

func (m Model) viewWorkspaceTooSmall() string {
	body := consoleSection.Render("KUBELOOP") + "\n\nTerminal is too small.\n" + fmt.Sprintf("Current: %dx%d  Required: %dx%d", m.width, m.height, workspaceMinWidth, workspaceMinHeight) + "\n\nResize the terminal or press q to quit."
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, consoleOverlayBox.Copy().Width(max(40, m.width-8)).Render(body))
}

func (m Model) viewWorkspaceHeader() string {
	server := truncateConsole(firstNonEmpty(m.activeProfile.BaseURL, "-"), 25)
	user := truncateConsole(firstNonEmpty(m.authSession.UserName, "-"), 25)
	namespace := truncateConsole(firstNonEmpty(m.namespace, "all"), 25)
	field := func(name, text string) string {
		return consoleSection.Render(name+":") + " " + consoleValue.Copy().Bold(true).Render(text)
	}
	left := strings.Join([]string{
		field("Cluster", server),
		field("User", user),
		field("Namespace", namespace),
		field("Mode", strings.ToUpper(string(m.selectedMode))),
		field("KubeLoop Rev", workspaceBuildRevision(m.version)),
		field("K8s Rev", "n/a"),
	}, "\n")

	keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#21B8FF")).Bold(true)
	namespaceKeyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FF00D7")).Bold(true)
	shortcut := func(key, text string) string {
		style := keyStyle
		if key == "0" || key == "1" {
			style = namespaceKeyStyle
		}
		return style.Render("<"+key+">") + " " + consoleSubtle.Render(text)
	}
	leftWidth := minInt(34, max(28, m.width/4))
	logoWidth := 0
	if m.width >= 120 {
		logoWidth = 28
	}
	shortcutWidth := max(1, m.width-leftWidth-logoWidth)
	shortcutColumnWidth := max(22, shortcutWidth/3)
	shortcutRow := func(first, second, third string) string {
		column := lipgloss.NewStyle().Width(shortcutColumnWidth).MaxWidth(shortcutColumnWidth)
		row := column.Render(first) + column.Render(second)
		if third != "" {
			row += column.Render(third)
		}
		return row
	}
	var shortcuts string
	if m.workspace.resource == resourceConnection {
		connectionAction := "Connect"
		if m.connected() {
			connectionAction = "Disconnect"
		}
		shortcuts = strings.Join([]string{
			shortcutRow(shortcut("a", "Add Server"), shortcut("?", "Help"), shortcut("r", "Refresh")),
			shortcutRow(shortcut("c", connectionAction), shortcut("q", "Quit"), shortcut("-", "Back")),
			shortcutRow(shortcut("p", "Pods"), shortcut("v", "Services"), shortcut("n", "Namespaces")),
			shortcutRow(shortcut("s", "Sessions"), shortcut(":", "Command"), shortcut("m", "Mode")),
			shortcutRow(shortcut("enter", "Toggle"), shortcut("]", "Forward"), shortcut("esc", "Cancel")),
			shortcutRow(shortcut("L", "Logout"), shortcut("u", "Uninstall Service"), ""),
		}, "\n")
	} else {
		rows := [][3]string{}
		namespaceRows := func() {
			rows = append(rows,
				[3]string{shortcut("0", "all"), shortcut("enter", "Describe"), ""},
				[3]string{shortcut("1", firstNonEmpty(m.activeProfile.LastNamespace, "default")), shortcut("n", "Namespace"), ""},
			)
		}
		switch m.workspace.resource {
		case resourcePods:
			namespaceRows()
			rows = append(rows,
				[3]string{shortcut("f", "Port-Forward"), shortcut("s", "SSH"), ""},
			)
		case resourceServices:
			namespaceRows()
			rows = append(rows,
				[3]string{shortcut("f", "Port-Forward"), shortcut("x", "Exchange"), shortcut("m", "Mirror")},
				[3]string{shortcut("p", "Preview"), "", ""},
			)
		case resourceTasks:
			rows = append(rows,
				[3]string{shortcut("enter", "Describe"), shortcut("d", "Stop"), shortcut("y", "Copy")},
				[3]string{shortcut("e", "Rerun"), shortcut("C", "Clear"), ""},
			)
		case resourceProfiles:
			rows = append(rows,
				[3]string{shortcut("enter", "Select"), shortcut("a", "Add"), shortcut("l", "Login")},
				[3]string{shortcut("d", "Delete"), shortcut("L", "Logout"), ""},
			)
		case resourceNamespaces:
			rows = append(rows, [3]string{shortcut("enter", "Select"), shortcut("/", "Filter"), ""})
		}
		if m.workspace.resource != resourceNamespaces {
			rows = append(rows, [3]string{shortcut(":", "Command"), shortcut("/", "Filter"), shortcut("r", "Refresh")})
		} else {
			rows = append(rows, [3]string{shortcut(":", "Command"), shortcut("r", "Refresh"), ""})
		}
		rows = append(rows,
			[3]string{shortcut("?", "Help"), shortcut("q", "Quit"), shortcut("esc", "Cancel")},
			[3]string{shortcut("-", "Back"), shortcut("]", "Forward"), ""},
		)
		rendered := make([]string, 0, len(rows))
		for _, row := range rows {
			rendered = append(rendered, shortcutRow(row[0], row[1], row[2]))
		}
		shortcuts = strings.Join(rendered, "\n")
	}

	if m.width < 96 {
		compact := field("NS", namespace)
		keys := shortcut(":", "Cmd")
		switch m.workspace.resource {
		case resourceConnection:
			keys = shortcut("c", "Connect") + "  " + shortcut("m", "Mode")
		case resourcePods:
			keys = shortcut("f", "Forward") + "  " + shortcut("s", "SSH") + "  " + keys
		case resourceServices:
			keys = shortcut("f", "Forward") + "  " + shortcut("x", "Exchange") + "  " + keys
		case resourceTasks:
			keys = shortcut("d", "Stop") + "  " + shortcut("y", "Copy") + "  " + keys
		case resourceProfiles:
			keys = shortcut("a", "Add") + "  " + shortcut("l", "Login") + "  " + keys
		case resourceNamespaces:
			keys = shortcut("enter", "Select") + "  " + keys
		}
		return lipgloss.JoinVertical(lipgloss.Left, compact, keys)
	}

	if m.width < 120 {
		return lipgloss.JoinHorizontal(lipgloss.Top,
			lipgloss.NewStyle().Width(leftWidth).Render(left),
			lipgloss.NewStyle().Width(max(1, m.width-leftWidth)).Render(shortcuts),
		)
	}
	logo := consoleSection.Render(" _  __ _   _ ____  _____\n" +
		"| |/ /| | | | __ )| ____|\n" +
		"| ' / | | | |  _ \\|  _|\n" +
		"| . \\ | |_| | |_) | |___\n" +
		"|_|\\_\\ \\___/|____/|_____|\n" +
		"        KUBELOOP\n")
	middleWidth := max(42, m.width-leftWidth-logoWidth)
	return lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.NewStyle().Width(leftWidth).Render(left),
		lipgloss.NewStyle().Width(middleWidth).Render(shortcuts),
		lipgloss.NewStyle().Width(logoWidth).Render(logo),
	)
}

func workspaceBuildRevision(version string) string {
	version = strings.TrimSpace(version)
	if version == "" || version == "(devel)" {
		return "dev"
	}
	return truncateConsole(version, 20)
}

func (m Model) viewWorkspaceCommandBar() string {
	if m.workspace.input != workspaceInputCommand && m.workspace.input != workspaceInputFilter {
		return ""
	}
	prompt := consoleSection.Render("🐶>")
	input := m.workspace.inputText
	if m.workspace.input == workspaceInputFilter {
		input = "/" + input
	}
	line := prompt + " " + consoleCmdText.Render(input+"█")
	return lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(consoleTeal).
		Padding(0, 1).
		Width(max(24, m.width-4)).
		Render(line)
}

func (m Model) viewWorkspaceBody(height int) string {
	if m.workspace.resource == resourceConnection {
		return m.viewWorkspaceConnection(height)
	}
	view := m.workspaceView()
	if view.detail {
		return m.viewWorkspaceDetail(height)
	}
	rows := m.workspaceFilteredRows()
	filter := view.filter
	heading := workspaceDescriptor(m.workspace.resource).title
	if m.workspace.resource == resourcePods || m.workspace.resource == resourceServices {
		heading += "(" + firstNonEmpty(m.namespace, "all") + ")"
	}
	heading += fmt.Sprintf("[%d]", len(rows))
	if filter != "" {
		heading += "   Filter: /" + filter
	}
	innerWidth := max(30, m.width-2)
	heading = truncateConsole(heading, max(10, innerWidth-4))
	titleWidth := lipgloss.Width(heading)
	leftRule := max(0, (innerWidth-titleWidth-2)/2)
	rightRule := max(0, innerWidth-titleWidth-leftRule-2)
	border := lipgloss.NewStyle().Foreground(consoleTeal).Background(lipgloss.Color("#000000"))
	rowStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#8BD5FF")).Background(lipgloss.Color("#000000"))
	headerStyle := rowStyle.Copy().Bold(true)
	selectedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#071018")).Background(lipgloss.Color("#7CC9F2")).Bold(true)
	top := border.Render("┌"+strings.Repeat("─", leftRule)+" ") + consoleOK.Render(heading) + border.Render(" "+strings.Repeat("─", rightRule)+"┐")
	lines := []string{top, border.Render("│") + headerStyle.Render(workspacePadLine(m.workspaceTableHeader(), innerWidth)) + border.Render("│")}
	page := max(1, height-3)
	if view.cursor < view.offset {
		view.offset = view.cursor
	}
	if view.cursor >= view.offset+page {
		view.offset = view.cursor - page + 1
	}
	end := minInt(len(rows), view.offset+page)
	for index := view.offset; index < end; index++ {
		line := workspacePadLine(m.workspaceTableRow(rows[index]), innerWidth)
		if index == view.cursor {
			line = selectedStyle.Render(line)
		} else {
			line = rowStyle.Render(line)
		}
		lines = append(lines, border.Render("│")+line+border.Render("│"))
	}
	if len(rows) == 0 {
		message := "No resources found."
		if filter != "" {
			message = "No matches for /" + filter
		}
		lines = append(lines, border.Render("│")+rowStyle.Render(workspacePadLine(" "+message, innerWidth))+border.Render("│"))
	}
	for len(lines) < height-1 {
		lines = append(lines, border.Render("│")+rowStyle.Render(strings.Repeat(" ", innerWidth))+border.Render("│"))
	}
	if len(lines) > height-1 {
		lines = lines[:height-1]
	}
	lines = append(lines, border.Render("└"+strings.Repeat("─", innerWidth)+"┘"))
	return strings.Join(lines, "\n")
}

func workspacePadLine(value string, width int) string {
	value = truncateConsole(value, width)
	return value + strings.Repeat(" ", max(0, width-lipgloss.Width(value)))
}

func (m Model) workspaceSelectionSummary(rows []consoleRow, view workspaceViewState) string {
	if len(rows) == 0 || view.cursor < 0 || view.cursor >= len(rows) {
		return consoleSubtle.Render("No resource selected")
	}
	row := rows[view.cursor]
	parts := []string{consoleValue.Render(row.title)}
	if row.status != "" {
		parts = append(parts, row.status)
	}
	if row.meta != "" {
		parts = append(parts, row.meta)
	}
	return truncateConsole(strings.Join(parts, "  "), max(20, m.width-8))
}

func (m Model) workspaceTableHeader() string {
	wide := m.width >= 100
	switch m.workspace.resource {
	case resourcePods:
		if wide {
			namespace, name, pf, ready, status, restarts, address, node, age := m.workspacePodColumnWidths()
			return fmt.Sprintf("%-*s %-*s %-*s %-*s %-*s %-*s %-*s %-*s %-*s", namespace, "NAMESPACE", name, "NAME", pf, "PF", ready, "READY", status, "STATUS", restarts, "RESTARTS", address, "POD IP", node, "NODE", age, "AGE")
		}
		return fmt.Sprintf("%-16s %-24s %-7s %s", "NAMESPACE", "NAME", "READY", "STATUS")
	case resourceServices:
		if wide {
			namespace, name, kind, clusterIP, externalIP, ports, age := m.workspaceServiceColumnWidths()
			return fmt.Sprintf("%-*s %-*s %-*s %-*s %-*s %-*s %-*s", namespace, "NAMESPACE", name, "NAME", kind, "TYPE", clusterIP, "CLUSTER-IP", externalIP, "EXTERNAL-IP", ports, "PORTS", age, "AGE")
		}
		return fmt.Sprintf("%-16s %-24s %-14s %s", "NAMESPACE", "NAME", "TYPE", "PORTS")
	case resourceTasks:
		return consoleSubtle.Render(fmt.Sprintf("  %-10s %-30s %-36s %s", "TYPE", "TARGET", "COMMAND / ADDRESS", "STATE"))
	case resourceProfiles:
		return consoleSubtle.Render(fmt.Sprintf("  %-24s %-52s %s", "NAME", "ENDPOINT", "STATUS"))
	case resourceNamespaces:
		return consoleSubtle.Render(fmt.Sprintf("  %-48s %s", "NAME", "STATUS"))
	}
	return ""
}

func (m Model) workspaceTableRow(row consoleRow) string {
	wide := m.width >= 100
	switch m.workspace.resource {
	case resourcePods:
		ready := workspaceMetaValue(row.meta, "ready")
		node := workspaceMetaValue(row.meta, "node")
		namespace := workspaceDetailValue(row.detail, "Namespace")
		ip := workspaceDetailValue(row.detail, "Pod IP")
		if wide {
			namespaceWidth, nameWidth, pfWidth, readyWidth, statusWidth, restartsWidth, addressWidth, nodeWidth, ageWidth := m.workspacePodColumnWidths()
			pf := m.workspacePodForwardMark(namespace, row.title)
			return fmt.Sprintf("%-*s %-*s %-*s %-*s %-*s %-*s %-*s %-*s %-*s", namespaceWidth, truncateConsole(namespace, namespaceWidth), nameWidth, truncateConsole(row.title, nameWidth), pfWidth, pf, readyWidth, truncateConsole(ready, readyWidth), statusWidth, truncateConsole(row.status, statusWidth), restartsWidth, workspaceMetaValue(row.meta, "restarts"), addressWidth, truncateConsole(ip, addressWidth), nodeWidth, truncateConsole(node, nodeWidth), ageWidth, workspaceMetaValue(row.meta, "age"))
		}
		return fmt.Sprintf("%-16s %-24s %-7s %s", truncateConsole(namespace, 16), truncateConsole(row.title, 24), ready, truncateConsole(row.status, 12))
	case resourceServices:
		namespace := workspaceDetailValue(row.detail, "Namespace")
		ports := strings.TrimPrefix(row.meta, "ports ")
		if wide {
			namespaceWidth, nameWidth, kindWidth, clusterWidth, externalWidth, portsWidth, ageWidth := m.workspaceServiceColumnWidths()
			return fmt.Sprintf("%-*s %-*s %-*s %-*s %-*s %-*s %-*s", namespaceWidth, truncateConsole(namespace, namespaceWidth), nameWidth, truncateConsole(row.title, nameWidth), kindWidth, truncateConsole(row.status, kindWidth), clusterWidth, truncateConsole(workspaceDetailValue(row.detail, "Cluster IP"), clusterWidth), externalWidth, truncateConsole(workspaceDetailValue(row.detail, "External IP"), externalWidth), portsWidth, truncateConsole(ports, portsWidth), ageWidth, workspaceDetailValue(row.detail, "Age"))
		}
		return fmt.Sprintf("%-16s %-24s %-14s %s", truncateConsole(namespace, 16), truncateConsole(row.title, 24), truncateConsole(row.status, 14), ports)
	case resourceTasks:
		return fmt.Sprintf("%-10s %-30s %-36s %s", row.status, truncateConsole(row.title, 30), truncateConsole(row.meta, 36), workspaceDetailValue(row.detail, "State"))
	case resourceProfiles:
		return fmt.Sprintf("%-24s %-52s %s", truncateConsole(row.title, 24), truncateConsole(row.meta, 52), row.status)
	case resourceNamespaces:
		return fmt.Sprintf("%-48s %s", truncateConsole(row.title, 48), row.status)
	}
	return row.title
}

func (m Model) workspacePodColumnWidths() (namespace, name, pf, ready, status, restarts, address, node, age int) {
	width := max(98, m.width-2)
	namespace = m.workspaceNamespaceColumnWidth()
	pf, ready, status, restarts, address, node, age = 2, 5, 10, 8, 14, 12, 5
	name = max(12, width-namespace-pf-ready-status-restarts-address-node-age-8)
	return
}

func (m Model) workspaceServiceColumnWidths() (namespace, name, kind, clusterIP, externalIP, ports, age int) {
	width := max(98, m.width-2)
	namespace = m.workspaceNamespaceColumnWidth()
	kind, clusterIP, externalIP, age = 11, 15, 15, 5
	remaining := max(26, width-namespace-kind-clusterIP-externalIP-age-6)
	name = max(18, remaining*55/100)
	ports = max(8, remaining-name)
	return
}

func (m Model) workspaceNamespaceColumnWidth() int {
	width := lipgloss.Width("NAMESPACE")
	switch m.workspace.resource {
	case resourcePods:
		for _, pod := range m.pods {
			width = max(width, lipgloss.Width(pod.Namespace))
		}
	case resourceServices:
		for _, service := range m.services {
			width = max(width, lipgloss.Width(service.Namespace))
		}
	}
	return minInt(22, max(14, width+1))
}

func (m Model) workspacePodForwardMark(namespace, name string) string {
	return "●"
}

func formatResourceAge(seconds int64) string {
	switch {
	case seconds <= 0:
		return "-"
	case seconds >= 86400:
		return fmt.Sprintf("%dd", seconds/86400)
	case seconds >= 3600:
		return fmt.Sprintf("%dh", seconds/3600)
	case seconds >= 60:
		return fmt.Sprintf("%dm", seconds/60)
	default:
		return fmt.Sprintf("%ds", max(0, int(seconds)))
	}
}

func workspaceMetaValue(meta, key string) string {
	fields := strings.Fields(meta)
	for index := range fields {
		if fields[index] == key && index+1 < len(fields) {
			return fields[index+1]
		}
	}
	return "-"
}

func workspaceDetailValue(detail, key string) string {
	prefix := key + ":"
	for line := range strings.SplitSeq(detail, "\n") {
		if after, ok := strings.CutPrefix(line, prefix); ok {
			return strings.TrimSpace(after)
		}
	}
	return "-"
}

func (m Model) viewWorkspaceDetail(height int) string {
	rows := m.workspaceFilteredRows()
	view := m.workspaceView()
	if len(rows) == 0 || view.cursor >= len(rows) {
		return consoleSubtle.Render("No selected resource.")
	}
	row := rows[view.cursor]
	descriptor := workspaceDescriptor(m.workspace.resource)
	breadcrumb := string(m.workspace.resource) + " > " + row.title
	innerWidth := max(30, m.width-8)
	bodyHeight := max(5, height-8)
	body := cropConsoleText(firstNonEmpty(row.detail, "No additional details."), 0, bodyHeight-2)
	details := consoleCard.Copy().Width(innerWidth).Height(bodyHeight).Render(
		consoleSection.Render("DETAILS") + "\n\n" + consoleValue.Render(body),
	)
	actionText := descriptor.actions
	if m.workspace.resource == resourceServices {
		actionText = strings.ReplaceAll(actionText, "  p preview", "")
	}
	actions := consoleDetail.Copy().Width(innerWidth).Render(
		consoleSection.Render("AVAILABLE ACTIONS") + "\n" + actionText,
	)
	title := consoleSection.Render(descriptor.title+" / "+row.title) + "\n" + consoleSubtle.Render(row.meta)
	return lipgloss.JoinVertical(lipgloss.Left, title, details, actions, consoleSubtle.Render(breadcrumb))
}

func (m Model) viewWorkspaceConnection(height int) string {
	state := "Disconnected"
	connecting := m.loading && strings.HasPrefix(m.status, "[")
	if connecting {
		state = "Connecting"
	}
	if m.connected() {
		state = "Connected"
	}
	server := firstNonEmpty(m.activeProfile.DisplayName, m.activeProfile.BaseURL, "Not selected")
	endpoint := firstNonEmpty(m.activeProfile.BaseURL, "-")
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#21B8FF")).Bold(true).Width(16)
	valueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#8BD5FF")).Bold(true)
	field := func(label, value string) string {
		return labelStyle.Render(label) + valueStyle.Render(value) + "\n\n"
	}
	stateStyle := consoleError
	if connecting {
		stateStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFB454"))
	}
	if m.connected() {
		stateStyle = consoleOK
	}
	leftBody := field("SERVER", server) +
		field("ENDPOINT", endpoint) +
		field("USER", firstNonEmpty(m.authSession.UserName, "-"))
	rightBody := labelStyle.Render("STATE") + stateStyle.Copy().Bold(true).Render(state) + "\n\n" +
		field("MODE", strings.ToUpper(string(m.selectedMode))) +
		field("SESSIONS", fmt.Sprint(m.consoleTaskCount()))
	panelHeight := max(8, height-3)
	panel := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#8BD5FF")).
		Background(lipgloss.Color("#000000")).
		Border(lipgloss.NormalBorder()).
		BorderForeground(consoleTeal).
		Padding(1, 2).
		Width(max(30, m.width-4)).
		Height(panelHeight)
	if m.width >= 82 {
		columnWidth := max(26, m.width/2-7)
		left := lipgloss.NewStyle().Width(columnWidth).Render(leftBody)
		right := lipgloss.NewStyle().Width(columnWidth).Render(rightBody)
		body := lipgloss.JoinHorizontal(lipgloss.Top, left, "  ", right)
		return panel.Render(consoleOK.Render("connection") + "\n\n" + body)
	}
	body := leftBody + "\n" + rightBody
	return panel.Render(consoleOK.Render("connection") + "\n\n" + body)
}

func (m Model) viewWorkspaceFooter() string {
	left := "<" + string(m.workspace.resource) + ">"
	inputHints := "  : command  ? help"
	if m.workspace.resource != resourceConnection {
		inputHints = "  / filter" + inputHints
	}
	right := workspaceDescriptor(m.workspace.resource).actions + inputHints
	if m.workspace.resource == resourceTasks {
		right = "d stop  y copy  / filter  : command  ? help"
	}
	lines := []string{}
	if m.workspace.warning != "" {
		lines = append(lines, consoleError.Render("Config: "+truncateConsole(m.workspace.warning, m.width-10)))
	}
	if m.loading {
		left = m.spinner.View() + " Working"
	} else if m.err != "" && m.workspace.input != workspaceInputNone {
		left = consoleError.Render(truncateConsole(m.err, max(20, m.width-20)))
	} else if m.status != "" {
		left = consoleOK.Render(truncateConsole(m.status, max(20, m.width-20)))
	}
	if m.workspace.input == workspaceInputCommand {
		left = consoleSection.Render("COMMAND")
		right = "Tab complete   ↑/↓ history   Enter run   Esc cancel"
	} else if m.workspace.input == workspaceInputFilter {
		left = consoleSection.Render("FILTER")
		right = "RE2   ! inverse   -f fuzzy   Enter keep   Esc cancel"
	}
	if m.width < 86 {
		shortcuts := "  : ?"
		if m.workspace.resource != resourceConnection {
			shortcuts = "  / : ?"
		}
		right = workspaceDescriptor(m.workspace.resource).actions + shortcuts
		if m.workspace.resource == resourceTasks {
			right = "d stop  y copy  / : ?"
		}
	}
	gap := strings.Repeat(" ", max(1, m.width-lipgloss.Width(left)-lipgloss.Width(right)-2))
	lines = append(lines, lipgloss.NewStyle().Width(m.width).Foreground(consoleDim).Background(consolePanel).Padding(0, 1).Render(left+gap+right))
	return strings.Join(lines, "\n")
}

func (m Model) viewWorkspaceHelp() string {
	content := consoleSection.Render("K9S WORKSPACE HELP") + "\n\n" +
		": command     / filter       ? help        q back/quit\n" +
		"j/k move      g/G first/last  Ctrl+u/d page  Enter inspect\n" +
		"- or [ back   ] forward       r refresh\n\n" +
		"RESOURCES\n  :connection  :pods  :services  :sessions  :servers  :namespaces/:ns\n\n" +
		"ACTIONS\n  :connect  :disconnect  :logout  :uninstall-service\n\n" +
		"FILTERS\n  /pattern RE2   /!pattern inverse   /-f text fuzzy\n\n" +
		"CURRENT ACTIONS\n  " + workspaceDescriptor(m.workspace.resource).actions
	box := consoleOverlayBox.Copy().Width(minInt(82, m.width-8)).Render(content)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func trimWorkspaceLastRune(value string) string {
	if value == "" {
		return ""
	}
	_, size := utf8.DecodeLastRuneInString(value)
	return value[:len(value)-size]
}
