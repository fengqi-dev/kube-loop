package tui

import tea "github.com/charmbracelet/bubbletea"

func (m Model) loadWorkspaceData() tea.Cmd {
	switch m.workspace.resource {
	case resourceProfiles:
		return loadProfiles(m.state)
	case resourceNamespaces:
		return loadNamespaces(m)
	case resourceConnection, resourcePods, resourceServices, resourceTasks:
		return m.loadTabData()
	}
	return nil
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
		commands = make([]tea.Cmd, 0, 2)
		commands = append(commands, wrap(loadNamespaces(*m)), wrap(loadPods(*m)))
	case resourceServices:
		commands = make([]tea.Cmd, 0, 2)
		commands = append(commands, wrap(loadNamespaces(*m)), wrap(loadServices(*m)))
	case resourceTasks:
		commands = []tea.Cmd{
			wrap(loadPortForwards(m.state, m.activeProfile.ID)),
			wrap(loadTrafficOperations(m.state, m.activeProfile.ID)),
			wrap(loadPodSSH(m.state, m.activeProfile.ID)),
		}
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
			m.login.adding = true
			m.login.url = ""
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
		case keyEnter, "c", "x":
			if m.connected() && m.consoleTaskCount() > 0 {
				m.console.overlay = overlayConfirmDisconnect
				return nil, true
			}
		}
		next, cmd := m.updateOverview(key)
		*m = requireModel(next)
		return cmd, true
	case resourcePods:
		if len(rows) == 0 {
			return nil, true
		}
		m.activeTab = tabWorkloads
		m.cursor = selected.index
		m.console.filters[tabWorkloads] = ""
		next, cmd := m.updatePods(key)
		*m = requireModel(next)
		return cmd, true
	case resourceServices:
		if key.String() == "p" {
			next, cmd := m.updateServices(key)
			*m = requireModel(next)
			return cmd, true
		}
		if len(rows) == 0 {
			return nil, true
		}
		m.activeTab = tabServices
		m.cursor = selected.index
		m.console.filters[tabServices] = ""
		next, cmd := m.updateServices(key)
		*m = requireModel(next)
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
			m.login.cursor = view.cursor
			m.console.returnTo = overlayNone
			m.console.overlay = overlayConfirmProfile
			return nil, true
		}
		m.login.cursor = view.cursor
		next, cmd := m.updateLogin(key)
		*m = requireModel(next)
		view.cursor = m.login.cursor
		m.setWorkspaceView(view)
		return cmd, true
	case resourceNamespaces:
		if key.String() == keyEnter && selected.title != "" {
			m.namespaceReturnResource = resourcePods
			return m.beginNamespaceSwitch(selected.title), true
		}
	}
	return nil, true
}

func (m Model) workspaceLegacyTaskPosition(selected consoleRow) int {
	clone := m
	clone.console.filters[tabTasks] = ""
	for position, row := range clone.consoleTaskRows() {
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
