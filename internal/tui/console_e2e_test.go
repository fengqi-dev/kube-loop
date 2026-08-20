package tui

import (
	"os"
	"reflect"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	clientdataplane "github.com/fengqi-dev/kube-loop/internal/client/dataplane"
	clientprofile "github.com/fengqi-dev/kube-loop/internal/client/profile"
	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
)

const tuiFixtureEnvironment = "KUBELOOP_TUI_E2E_FIXTURE"

func TestTUIFixture(t *testing.T) {
	if os.Getenv(tuiFixtureEnvironment) != "1" {
		t.Skip("run through e2e/tui/run.sh")
	}

	model := newConsoleTestModel(tabConnection, 120, 32)
	model.selectedMode = clientdataplane.ModeTUN
	model.workspace = workspaceState{
		initialized: true, resource: resourceConnection,
		views: map[workspaceResource]workspaceViewState{}, history: []workspaceResource{resourceConnection},
		config: workspaceConfig{Version: 1, Aliases: map[string]string{}, Hotkeys: map[string]string{}},
	}
	model.dataPlaneStatus = clientdataplane.Status{}
	model.namespaces = []clientremote.Namespace{
		{Name: "default"},
		{Name: "production"},
		{Name: "preview"},
	}
	model.pods = []clientremote.Pod{{
		Name: "api-0", Namespace: "default", Phase: "Running", PodIP: "10.0.0.12", NodeName: "worker-1", Ready: true,
		Containers: []string{"api"}, Ports: []clientremote.PodPort{{Name: "http", Port: 8080, Protocol: "TCP"}},
	}}
	model.services = []clientremote.Service{{
		Name: "api", Namespace: "default", Type: "ClusterIP", ClusterIP: "10.96.0.20",
		Ports: []clientremote.ServicePort{{Name: "http", Port: 80, Protocol: "TCP", TargetPort: "8080"}},
	}}
	model.execTasks = []execTaskView{
		{ID: "exec-test", Pod: "api-0", Command: "env", State: "running", Output: "READY=true"},
		{ID: "exec-complete", Pod: "worker-0", Command: "true", State: "completed", Output: "done"},
	}

	program := tea.NewProgram(
		tuiFixtureModel{model: model},
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	if _, err := program.Run(); err != nil {
		t.Fatalf("run TUI fixture: %v", err)
	}
}

func TestTUIProfileFixture(t *testing.T) {
	if os.Getenv(tuiFixtureEnvironment) != "1" {
		t.Skip("run through e2e/tui/run.sh")
	}

	model := newConsoleTestModel(tabConnection, 100, 28)
	model.mode = viewLogin
	model.workspace = workspaceState{
		initialized: true, resource: resourceProfiles,
		views: map[workspaceResource]workspaceViewState{}, history: []workspaceResource{resourceProfiles},
		config: workspaceConfig{Version: 1, Aliases: map[string]string{}, Hotkeys: map[string]string{}},
	}
	model.profiles = clientprofile.State{Profiles: []clientprofile.Profile{
		{ID: "primary", DisplayName: "Primary", BaseURL: "https://primary.example.test"},
		{ID: "staging", DisplayName: "Staging", BaseURL: "https://staging.example.test"},
	}}
	model.activeProfile = model.profiles.Profiles[0]

	program := tea.NewProgram(
		tuiFixtureModel{model: model},
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	if _, err := program.Run(); err != nil {
		t.Fatalf("run TUI profile fixture: %v", err)
	}
}

type tuiFixtureModel struct {
	model Model
}

func (m tuiFixtureModel) Init() tea.Cmd {
	return nil
}

func (m tuiFixtureModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		m.model.width, m.model.height = message.Width, message.Height
		return m, nil
	case tea.KeyMsg:
		if message.String() == "ctrl+c" {
			return m, tea.Quit
		}
	}

	key, isKey := message.(tea.KeyMsg)
	previousOverlay := m.model.console.overlay
	selectedTask, selectedTaskOK := m.model.selectedConsoleTask()
	previousResource := m.model.workspace.resource
	if cmd, handled := m.model.updateWorkspace(message); handled {
		if isKey && (key.String() == "enter" || key.String() == "y") {
			switch previousOverlay {
			case overlayConfirmTask:
				if selectedTaskOK && selectedTask.kind == "exec" {
					index := selectedTask.index - len(m.model.portForwards) - len(m.model.podSSHEndpoints)
					if index >= 0 && index < len(m.model.execTasks) {
						m.model.execTasks[index].State = "stopped"
					}
				}
				m.model.status = "Session stopped"
			case overlayConfirmDisconnect:
				m.model.dataPlaneStatus = clientdataplane.Status{}
				m.model.status = "Data plane disconnected"
			}
		}
		if isKey && previousResource == resourceNamespaces && key.String() == "enter" {
			rows := m.model.workspaceFilteredRows()
			view := m.model.workspaceView()
			if view.cursor < len(rows) {
				m.model.namespace = rows[view.cursor].title
				m.model.workspace.resource = resourcePods
				m.model.activeTab = tabWorkloads
			}
		}
		if isKey && previousResource == resourceConnection {
			switch key.String() {
			case "m":
				m.model.status = "Mode switched to " + string(m.model.selectedMode)
			case "enter", "c", "x":
				if previousOverlay == overlayNone && m.model.console.overlay == overlayNone {
					m.model.dataPlaneStatus = clientdataplane.Status{State: "connected", Mode: string(m.model.selectedMode)}
					m.model.status = "Data plane connected"
				}
			}
		}
		m.model.loading = false
		if isConsoleQuitCommand(cmd) {
			return m, tea.Quit
		}
		return m, nil
	}
	if mouse, ok := message.(tea.MouseMsg); ok {
		_ = mouse
		return m, nil
	}
	if !isKey {
		return m, nil
	}

	if m.model.actionMode != actionNone {
		if key.String() == "enter" {
			if m.model.actionMode == actionExec {
				m.model.status = "Pod exec started"
			} else {
				m.model.status = "Port forward started"
			}
			m.model.actionMode = actionNone
			m.model.loading = false
			return m, nil
		}
		next, _ := m.model.updateAction(key)
		m.model = next.(Model)
		return m, nil
	}

	if m.model.mode == viewLogin {
		next, _ := m.model.updateLogin(key)
		m.model = next.(Model)
		return m, nil
	}

	switch m.model.activeTab {
	case tabConnection:
		switch key.String() {
		case "m":
			if m.model.selectedMode == clientdataplane.ModeTUN {
				m.model.selectedMode = clientdataplane.ModeSOCKS
			} else {
				m.model.selectedMode = clientdataplane.ModeTUN
			}
			m.model.status = "Mode switched to " + string(m.model.selectedMode)
		case "enter", "c", "x":
			m.model.dataPlaneStatus = clientdataplane.Status{State: "connected", Mode: string(m.model.selectedMode)}
			m.model.status = "Data plane connected"
		}
	case tabWorkloads:
		next, _ := m.model.updatePods(key)
		m.model = next.(Model)
	case tabServices:
		next, _ := m.model.updateServices(key)
		m.model = next.(Model)
	}
	return m, nil
}

func (m tuiFixtureModel) View() string {
	return m.model.View()
}

// isConsoleQuitCommand reports whether cmd is tea.Quit without executing it.
// The fixture model has a nil State, so running any other command would panic
// or spawn goroutines that hit a real server.
func isConsoleQuitCommand(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	return reflect.ValueOf(cmd).Pointer() == reflect.ValueOf(tea.Quit).Pointer()
}
