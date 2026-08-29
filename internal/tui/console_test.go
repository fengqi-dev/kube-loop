package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	clientdataplane "github.com/fengqi-dev/kube-loop/internal/client/dataplane"
	clientexchange "github.com/fengqi-dev/kube-loop/internal/client/exchange"
	clientmirror "github.com/fengqi-dev/kube-loop/internal/client/mirror"
	clientpodssh "github.com/fengqi-dev/kube-loop/internal/client/podssh"
	clientportforward "github.com/fengqi-dev/kube-loop/internal/client/portforward"
	clientpreview "github.com/fengqi-dev/kube-loop/internal/client/preview"
	clientprofile "github.com/fengqi-dev/kube-loop/internal/client/profile"
	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
)

func TestViewConsoleResponsiveLayouts(t *testing.T) {
	tests := []struct {
		name      string
		width     int
		height    int
		want      []string
		doNotWant []string
	}{
		{
			name:   "minimum size guard",
			width:  59,
			height: 17,
			want:   []string{"Terminal is too small", "Required: 60x18"},
		},
		{
			name:      "standard top navigation",
			width:     90,
			height:    24,
			want:      []string{"OPERATIONS CONSOLE", "Connection", "Workloads", "CONNECTION"},
			doNotWant: []string{"NAVIGATION"},
		},
		{
			name:   "wide sidebar navigation",
			width:  120,
			height: 32,
			want:   []string{"OPERATIONS CONSOLE", "NAVIGATION", "Namespace", "QUICK ACTIONS"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := newConsoleTestModel(tabConnection, test.width, test.height)
			view := model.View()
			assertConsoleContains(t, view, test.want...)
			for _, value := range test.doNotWant {
				if strings.Contains(view, value) {
					t.Fatalf("View() unexpectedly contains %q", value)
				}
			}
		})
	}
}

func TestViewConsoleCoreViews(t *testing.T) {
	tests := []struct {
		name  string
		model Model
		want  []string
	}{
		{
			name:  "connection",
			model: newConsoleTestModel(tabConnection, 120, 32),
			want:  []string{"CONNECTION", "Connected", "test-server", "SOCKS", "QUICK ACTIONS"},
		},
		{
			name: "workload list and details",
			model: func() Model {
				model := newConsoleTestModel(tabWorkloads, 120, 32)
				model.pods = []clientremote.Pod{{}}
				return model
			}(),
			want: []string{"WORKLOADS  1", "Unnamed pod", "DETAILS", "port forward", "SSH", "exec"},
		},
		{
			name: "service list and details",
			model: func() Model {
				model := newConsoleTestModel(tabServices, 120, 32)
				model.services = []clientremote.Service{{}}
				return model
			}(),
			want: []string{"SERVICES  1", "Unnamed service", "DETAILS", "start port forward"},
		},
		{
			name: "task list output details",
			model: func() Model {
				model := newConsoleTestModel(tabTasks, 120, 32)
				model.execTasks = []execTaskView{{Pod: "api-0", Command: "env", State: "running", Output: "READY=true"}}
				return model
			}(),
			want: []string{"SESSIONS · ALL  1", "api-0", "EXEC", "Command: env", "OUTPUT", "READY=true"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertConsoleContains(t, test.model.View(), test.want...)
		})
	}
}

func TestConsoleTaskRowsCoverAllKindsAndFilters(t *testing.T) {
	model := newConsoleTestModel(tabTasks, 120, 32)
	model.portForwards = []clientportforward.Info{{
		Name: "api", Namespace: "default", Kind: "service", Protocol: "tcp",
		Address: "127.0.0.1:8080", DialAddress: "10.0.0.1:8080", RemotePort: 8080, State: "running",
	}}
	model.exchanges = []clientexchange.Info{{
		Service: "exchange", Namespace: "default", ClusterIP: "10.96.0.10", State: "running",
	}}
	model.mirrors = []clientmirror.Info{{
		Service: "mirror", Namespace: "default", ClusterIP: "10.96.0.11", State: "running",
	}}
	model.previews = []clientpreview.Info{{
		Name: "preview", Namespace: "default", ClusterIP: "10.96.0.12", State: "running",
	}}
	model.podSSHEndpoints = []clientpodssh.Info{{
		Pod: "api-0", Namespace: "default", PodIP: "10.0.0.2", Address: "127.0.0.1:2222",
		Command: "sh", State: "running",
	}}
	model.execTasks = []execTaskView{{Pod: "api-0", Command: "env", State: "running", Output: "READY=true"}}

	rows := model.consoleTaskRows()
	expected := []string{"FORWARD", "EXCHANGE", "MIRROR", "PREVIEW", "SSH", "EXEC"}
	if len(rows) != len(expected) {
		t.Fatalf("task rows = %#v, want %d rows", rows, len(expected))
	}
	for index, status := range expected {
		if rows[index].status != status {
			t.Fatalf("row %d status = %q, want %q", index, rows[index].status, status)
		}
	}

	tests := []struct {
		name   string
		filter int
		count  int
	}{
		{name: "forward", filter: taskFilterForward, count: 1},
		{name: "traffic", filter: taskFilterTraffic, count: 3},
		{name: "ssh", filter: taskFilterSSH, count: 1},
		{name: "exec", filter: taskFilterExec, count: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			filtered := model
			filtered.console.taskFilter = test.filter
			if got := len(filtered.consoleTaskRows()); got != test.count {
				t.Fatalf("filtered task rows = %d, want %d", got, test.count)
			}
		})
	}
}

func TestUpdateConsolePreservesCursorByView(t *testing.T) {
	model := newConsoleTestModel(tabWorkloads, 100, 28)
	model.pods = []clientremote.Pod{{}, {}, {}}
	model.services = []clientremote.Service{{}, {}}

	if _, handled := model.updateConsole(consoleKey("down")); !handled {
		t.Fatal("down key was not handled")
	}
	if model.cursor != 1 {
		t.Fatalf("workload cursor = %d, want 1", model.cursor)
	}
	if _, handled := model.updateConsole(tea.KeyMsg{Type: tea.KeyTab}); !handled {
		t.Fatal("tab key was not handled")
	}
	if model.activeTab != tabServices || model.cursor != 0 {
		t.Fatalf("after tab: activeTab=%d cursor=%d, want services/0", model.activeTab, model.cursor)
	}
	model.updateConsole(consoleKey("down"))
	model.updateConsole(tea.KeyMsg{Type: tea.KeyShiftTab})
	if model.activeTab != tabWorkloads || model.cursor != 1 {
		t.Fatalf("restored activeTab=%d cursor=%d, want workloads/1", model.activeTab, model.cursor)
	}
}

func TestUpdateConsoleOverlays(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		model := newConsoleTestModel(tabConnection, 100, 28)
		model.updateConsole(consoleKey("?"))
		if model.console.overlay != overlayHelp {
			t.Fatalf("overlay = %d, want help", model.console.overlay)
		}
		assertConsoleContains(t, model.View(), "KEYBOARD REFERENCE", "Change view", "Close help")
		model.updateConsole(consoleKey("esc"))
		if model.console.overlay != overlayNone {
			t.Fatalf("overlay = %d after escape, want none", model.console.overlay)
		}
	})

	t.Run("search namespaces", func(t *testing.T) {
		model := newConsoleTestModel(tabWorkloads, 100, 28)
		model.dataPlaneStatus = clientdataplane.Status{}
		model.namespaces = []clientremote.Namespace{{Name: "default"}, {Name: "production"}, {Name: "preview"}}
		model.updateConsole(consoleKey("n"))
		for _, value := range "prod" {
			model.updateConsole(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{value}})
		}
		matches := model.filteredNamespaces()
		if len(matches) != 1 || matches[0] != "production" {
			t.Fatalf("filteredNamespaces() = %v, want [production]", matches)
		}
		assertConsoleContains(t, model.View(), "SELECT NAMESPACE", "production")
	})

	t.Run("confirm task delete", func(t *testing.T) {
		model := newConsoleTestModel(tabTasks, 100, 28)
		model.execTasks = []execTaskView{{Pod: "api-0", State: "running"}}
		model.updateConsole(consoleKey("d"))
		if model.console.overlay != overlayConfirmTask {
			t.Fatalf("overlay = %d, want task confirmation", model.console.overlay)
		}
		assertConsoleContains(t, model.View(), "DELETE SESSION?", "Delete session")
		model.updateConsole(consoleKey("n"))
		if model.console.overlay != overlayNone {
			t.Fatalf("overlay = %d after cancel, want none", model.console.overlay)
		}
	})

	t.Run("confirm disconnect with active tasks", func(t *testing.T) {
		model := newConsoleTestModel(tabConnection, 100, 28)
		model.execTasks = []execTaskView{{Pod: "api-0", State: "running"}}
		model.updateConsole(consoleKey("x"))
		if model.console.overlay != overlayConfirmDisconnect {
			t.Fatalf("overlay = %d, want disconnect confirmation", model.console.overlay)
		}
		assertConsoleContains(t, model.View(), "DISCONNECT?", "1 active session(s)")
	})

	t.Run("confirm profile deletion", func(t *testing.T) {
		model := newConsoleTestModel(tabConnection, 100, 28)
		model.mode = viewLogin
		model.profiles = clientprofile.State{Profiles: []clientprofile.Profile{{ID: "test", DisplayName: "Test"}}}
		model.updateConsole(consoleKey("d"))
		if model.console.overlay != overlayConfirmProfile {
			t.Fatalf("overlay = %d, want profile confirmation", model.console.overlay)
		}
		assertConsoleContains(t, model.View(), "DELETE SERVER?", "Delete server")
	})
}

func TestUpdateConsoleMouseNavigation(t *testing.T) {
	tests := []struct {
		name    string
		width   int
		x       int
		y       int
		wantTab tab
	}{
		{name: "wide sidebar", width: 120, x: 4, y: 10, wantTab: tabTasks},
		{name: "standard top navigation", width: 90, x: 50, y: 3, wantTab: tabServices},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := newConsoleTestModel(tabConnection, test.width, 28)
			event := tea.MouseMsg(
				tea.MouseEvent{X: test.x, Y: test.y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft},
			)
			if _, handled := model.updateConsole(event); !handled {
				t.Fatal("mouse event was not handled")
			}
			if model.activeTab != test.wantTab {
				t.Fatalf("activeTab = %d, want %d", model.activeTab, test.wantTab)
			}
		})
	}
}

func TestViewConsoleActionOverlay(t *testing.T) {
	model := newConsoleTestModel(tabWorkloads, 100, 28)
	model.actionMode = actionExec
	model.actionPod = "api-0"
	model.actionCommand = "env"
	assertConsoleContains(t, model.View(), "EXECUTE COMMAND", "api-0", "Command: env", "Enter  Start")
}

func TestUpdateAddProfileAcceptsPasteAndUnicode(t *testing.T) {
	model := Model{loginAdding: true}
	next, _ := model.updateAddProfile(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("https://例子.test/path")})
	model = next.(Model)
	if model.loginURL != "https://例子.test/path" {
		t.Fatalf("loginURL = %q", model.loginURL)
	}
	next, _ = model.updateAddProfile(tea.KeyMsg{Type: tea.KeyBackspace})
	model = next.(Model)
	if model.loginURL != "https://例子.test/pat" {
		t.Fatalf("loginURL after backspace = %q", model.loginURL)
	}
}

func TestUpdateConsoleListMouseAndWheel(t *testing.T) {
	model := newConsoleTestModel(tabWorkloads, 120, 32)
	model.pods = []clientremote.Pod{{Name: "one"}, {Name: "two"}, {Name: "three"}}
	model.updateConsole(
		tea.MouseMsg(tea.MouseEvent{X: 30, Y: 9, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}),
	)
	if model.cursor != 1 {
		t.Fatalf("cursor after row click = %d, want 1", model.cursor)
	}
	model.updateConsole(
		tea.MouseMsg(tea.MouseEvent{X: 30, Y: 9, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown}),
	)
	if model.cursor != 2 {
		t.Fatalf("cursor after wheel = %d, want 2", model.cursor)
	}
}

func TestUpdateConsoleTaskFilterAndProfileOverlay(t *testing.T) {
	model := newConsoleTestModel(tabTasks, 120, 32)
	model.execTasks = []execTaskView{{Pod: "api-0", State: "running"}}
	model.updateConsole(consoleKey("t"))
	if model.console.taskFilter != taskFilterForward || model.consoleItemCount() != 0 {
		t.Fatalf("task filter=%d count=%d, want forward/0", model.console.taskFilter, model.consoleItemCount())
	}
	model.updateConsole(consoleKey("t"))
	model.updateConsole(consoleKey("t"))
	model.updateConsole(consoleKey("t"))
	if model.console.taskFilter != taskFilterExec || model.consoleItemCount() != 1 {
		t.Fatalf("task filter=%d count=%d, want exec/1", model.console.taskFilter, model.consoleItemCount())
	}

	model = newConsoleTestModel(tabConnection, 120, 32)
	model.dataPlaneStatus = clientdataplane.Status{}
	model.updateConsole(consoleKey("p"))
	if model.console.overlay != overlayProfiles {
		t.Fatalf("overlay=%d, want profiles", model.console.overlay)
	}
	assertConsoleContains(t, model.View(), "MANAGE SERVERS")
}

func TestConsoleProfileOverlayKeyboardAndMouse(t *testing.T) {
	profiles := []clientprofile.Profile{
		{ID: "first", DisplayName: "First", BaseURL: "https://first.example.test"},
		{ID: "second", DisplayName: "Second", BaseURL: "https://second.example.test"},
	}
	model := newConsoleTestModel(tabConnection, 120, 32)
	model.profiles = clientprofile.State{ActiveProfileID: "first", Profiles: profiles}
	model.console.overlay = overlayProfiles

	assertConsoleContains(t, model.viewConsoleProfilesOverlay(), "First", "Second", "Enter select")
	model.updateConsole(tea.MouseMsg(tea.MouseEvent{
		X: 20, Y: 16, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
	}))
	if model.loginCursor != 1 {
		t.Fatalf("profile cursor after overlay click = %d, want 1", model.loginCursor)
	}
	model.updateConsole(consoleKey("up"))
	if model.loginCursor != 0 {
		t.Fatalf("profile cursor after up = %d, want 0", model.loginCursor)
	}
	model.updateConsole(consoleKey("j"))
	if model.loginCursor != 1 {
		t.Fatalf("profile cursor after j = %d, want 1", model.loginCursor)
	}
	model.updateConsole(consoleKey("d"))
	if model.console.overlay != overlayConfirmProfile || model.console.returnTo != overlayProfiles {
		t.Fatalf("delete overlay=%d returnTo=%d", model.console.overlay, model.console.returnTo)
	}
	model.updateConsole(consoleKey("n"))
	if model.console.overlay != overlayProfiles {
		t.Fatalf("cancelled delete overlay = %d, want profiles", model.console.overlay)
	}
	model.updateConsole(consoleKey("a"))
	if model.console.overlay != overlayProfileAdd || !model.loginAdding {
		t.Fatalf("add overlay=%d loginAdding=%v", model.console.overlay, model.loginAdding)
	}
	model.console.overlay, model.loginAdding = overlayProfiles, false
	model.updateConsole(consoleKey("p"))
	if model.console.overlay != overlayNone {
		t.Fatalf("profile overlay after p = %d, want none", model.console.overlay)
	}

	login := newConsoleTestModel(tabConnection, 120, 32)
	login.mode = viewLogin
	login.profiles = clientprofile.State{Profiles: profiles}
	login.updateConsole(tea.MouseMsg(tea.MouseEvent{
		X: 10, Y: 7, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
	}))
	if login.loginCursor != 1 {
		t.Fatalf("login cursor after profile click = %d, want 1", login.loginCursor)
	}
	if _, handled := login.updateConsole(tea.MouseMsg(tea.MouseEvent{
		X: 10, Y: 20, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
	})); handled {
		t.Fatal("click outside login profiles was handled")
	}

	adding := newConsoleTestModel(tabConnection, 120, 32)
	adding.mode, adding.loginAdding, adding.loginURL = viewLogin, true, "https://new.example.test"
	if _, handled := adding.updateConsole(tea.MouseMsg(tea.MouseEvent{
		X: 10, Y: 10, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
	})); !handled || !adding.loginAdding {
		t.Fatalf("upper form click handled=%v loginAdding=%v", handled, adding.loginAdding)
	}
	if _, handled := adding.updateConsole(tea.MouseMsg(tea.MouseEvent{
		X: 80, Y: 20, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
	})); !handled || adding.loginAdding || adding.loginURL != "" {
		t.Fatalf("cancel click handled=%v loginAdding=%v url=%q", handled, adding.loginAdding, adding.loginURL)
	}

	empty := newConsoleTestModel(tabConnection, 120, 32)
	empty.profiles = clientprofile.State{}
	assertConsoleContains(t, empty.viewConsoleProfilesOverlay(), "No servers configured")
}

func TestConfirmConsoleOverlayActions(t *testing.T) {
	t.Run("profile deletion returns to profiles", func(t *testing.T) {
		model := newConsoleTestModel(tabConnection, 120, 32)
		model.mode = viewLogin
		model.profiles = clientprofile.State{Profiles: []clientprofile.Profile{{ID: "test"}}}
		model.console.overlay, model.console.returnTo = overlayConfirmProfile, overlayProfiles
		cmd := model.confirmConsoleOverlay()
		if cmd == nil || !model.loading || model.console.overlay != overlayProfiles {
			t.Fatalf("profile delete cmd=%v loading=%v overlay=%d", cmd != nil, model.loading, model.console.overlay)
		}
	})

	t.Run("disconnect", func(t *testing.T) {
		model := newConsoleTestModel(tabConnection, 120, 32)
		model.console.overlay = overlayConfirmDisconnect
		if cmd := model.confirmConsoleOverlay(); cmd == nil || !model.loading || model.status != "Disconnecting..." {
			t.Fatalf("disconnect cmd=%v loading=%v status=%q", cmd != nil, model.loading, model.status)
		}
	})

	t.Run("service uninstall", func(t *testing.T) {
		model := newConsoleTestModel(tabConnection, 120, 32)
		model.console.overlay = overlayConfirmServiceUninstall
		if cmd := model.confirmConsoleOverlay(); cmd == nil || !model.loading ||
			model.status != "Uninstalling Helper Service..." {
			t.Fatalf("uninstall cmd=%v loading=%v status=%q", cmd != nil, model.loading, model.status)
		}
	})

	t.Run("non-confirmation overlays are no-op", func(t *testing.T) {
		for _, overlay := range []consoleOverlay{
			overlayNone, overlayHelp, overlayNamespace, overlayProfiles, overlayProfileAdd,
		} {
			model := newConsoleTestModel(tabConnection, 120, 32)
			model.console.overlay = overlay
			if cmd := model.confirmConsoleOverlay(); cmd != nil || model.console.overlay != overlayNone {
				t.Fatalf("overlay %d cmd=%v resulting overlay=%d", overlay, cmd != nil, model.console.overlay)
			}
		}
	})
}

func TestUpdateConsoleDetailFocusAndCopyOutput(t *testing.T) {
	model := newConsoleTestModel(tabTasks, 120, 32)
	model.execTasks = []execTaskView{{Pod: "api-0", State: "running", Output: "READY=true"}}
	model.updateConsole(
		tea.MouseMsg(tea.MouseEvent{X: 100, Y: 9, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}),
	)
	if model.console.views[tabTasks].focus != focusDetail {
		t.Fatal("detail click did not focus the details panel")
	}
	cmd, handled := model.updateConsole(consoleKey("y"))
	if !handled || cmd == nil || model.status != "Copying EXEC session..." {
		t.Fatalf("handled=%v cmd=%v status=%q, want async copy", handled, cmd != nil, model.status)
	}
	updated, _ := model.Update(clipboardCopiedMsg{kind: "EXEC"})
	model = updated.(Model)
	if model.status != "EXEC session copied to clipboard" {
		t.Fatalf("status = %q, want copied status", model.status)
	}
}

func TestUpdateConsolePagingAndTaskActions(t *testing.T) {
	model := newConsoleTestModel(tabWorkloads, 100, 24)
	model.pods = make([]clientremote.Pod, 20)
	model.updateConsole(tea.KeyMsg{Type: tea.KeyPgDown})
	if model.cursor == 0 || model.console.views[tabWorkloads].offset == 0 {
		t.Fatalf("page down cursor=%d offset=%d", model.cursor, model.console.views[tabWorkloads].offset)
	}
	model.updateConsole(consoleKey("]"))
	model.updateConsole(consoleKey("down"))
	if model.console.views[tabWorkloads].detailOffset != 1 {
		t.Fatalf("detail offset=%d, want 1", model.console.views[tabWorkloads].detailOffset)
	}

	model = newConsoleTestModel(tabTasks, 120, 32)
	model.execTasks = []execTaskView{
		{Pod: "running", Command: "env", State: "running"},
		{Pod: "done", Command: "true", State: "completed"},
	}
	model.updateConsole(consoleKey("e"))
	if model.actionMode != actionExec || model.actionPod != "running" || model.actionCommand != "env" {
		t.Fatalf("exec rerun action=%d pod=%q command=%q", model.actionMode, model.actionPod, model.actionCommand)
	}
	model.actionMode = actionNone
	model.updateConsole(consoleKey("C"))
	if len(model.execTasks) != 1 || model.execTasks[0].Pod != "running" {
		t.Fatalf("exec tasks after clear=%v", model.execTasks)
	}
}

func TestUpdateConsoleMouseButtons(t *testing.T) {
	t.Run("connection button", func(t *testing.T) {
		model := newConsoleTestModel(tabConnection, 120, 32)
		cmd, handled := model.updateConsole(
			tea.MouseMsg(tea.MouseEvent{X: 90, Y: 7, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}),
		)
		if !handled || cmd == nil || !model.loading {
			t.Fatalf("handled=%v cmd=%v loading=%v", handled, cmd != nil, model.loading)
		}
	})

	t.Run("confirmation button", func(t *testing.T) {
		model := newConsoleTestModel(tabTasks, 120, 32)
		model.execTasks = []execTaskView{{ID: "exec", Pod: "api-0", State: "running"}}
		model.updateConsole(consoleKey("d"))
		cmd, handled := model.updateConsole(
			tea.MouseMsg(tea.MouseEvent{X: 30, Y: 19, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}),
		)
		if !handled || cmd == nil || model.console.overlay != overlayNone {
			t.Fatalf("handled=%v cmd=%v overlay=%d", handled, cmd != nil, model.console.overlay)
		}
	})

	t.Run("action cancel button", func(t *testing.T) {
		model := newConsoleTestModel(tabTasks, 120, 32)
		model.actionMode, model.actionPod = actionExec, "api-0"
		_, handled := model.updateConsole(
			tea.MouseMsg(tea.MouseEvent{X: 90, Y: 19, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}),
		)
		if !handled || model.actionMode != actionNone {
			t.Fatalf("handled=%v actionMode=%d", handled, model.actionMode)
		}
	})
}

func TestUpdateConsoleCommandMode(t *testing.T) {
	t.Run("opens command bar and renders input", func(t *testing.T) {
		model := newConsoleTestModel(tabConnection, 100, 28)
		if _, handled := model.updateConsole(consoleKey(":")); !handled {
			t.Fatal(": was not handled")
		}
		if model.console.inputMode != inputCommand {
			t.Fatalf("inputMode = %d, want command", model.console.inputMode)
		}
		model.updateConsole(consoleKey("p"))
		model.updateConsole(consoleKey("o"))
		if model.console.inputText != "po" {
			t.Fatalf("inputText = %q, want po", model.console.inputText)
		}
		assertConsoleContains(t, model.View(), "po█", "Enter run", "Esc cancel")
	})

	t.Run("navigation aliases", func(t *testing.T) {
		tests := []struct {
			command string
			want    tab
		}{
			{"pods", tabWorkloads}, {"po", tabWorkloads}, {"w", tabWorkloads},
			{"svc", tabServices}, {"services", tabServices},
			{"sessions", tabTasks}, {"fw", tabTasks},
			{"conn", tabConnection}, {"c", tabConnection},
		}
		for _, test := range tests {
			model := newConsoleTestModel((test.want+1)%tabCount, 100, 28)
			model.updateConsole(consoleKey(":"))
			for _, r := range test.command {
				model.updateConsole(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
			}
			cmd, handled := model.updateConsole(tea.KeyMsg{Type: tea.KeyEnter})
			if !handled {
				t.Fatalf("%q: enter was not handled", test.command)
			}
			if model.activeTab != test.want {
				t.Fatalf("%q: activeTab = %d, want %d", test.command, model.activeTab, test.want)
			}
			if cmd == nil {
				t.Fatalf("%q: expected a data-loading command", test.command)
			}
			if model.console.inputMode != inputNone {
				t.Fatalf("%q: inputMode = %d after enter, want none", test.command, model.console.inputMode)
			}
		}
	})

	t.Run("commands are case insensitive", func(t *testing.T) {
		model := newConsoleTestModel(tabConnection, 100, 28)
		model.updateConsole(consoleKey(":"))
		for _, r := range "PODS" {
			model.updateConsole(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
		model.updateConsole(tea.KeyMsg{Type: tea.KeyEnter})
		if model.activeTab != tabWorkloads {
			t.Fatalf("activeTab = %d, want workloads", model.activeTab)
		}
	})

	t.Run("esc cancels without side effects", func(t *testing.T) {
		model := newConsoleTestModel(tabConnection, 100, 28)
		model.updateConsole(consoleKey(":"))
		model.updateConsole(consoleKey("q"))
		model.updateConsole(tea.KeyMsg{Type: tea.KeyEsc})
		if model.console.inputMode != inputNone || model.console.inputText != "" {
			t.Fatalf("inputMode=%d inputText=%q after esc", model.console.inputMode, model.console.inputText)
		}
	})

	t.Run("unknown command reports error", func(t *testing.T) {
		model := newConsoleTestModel(tabConnection, 100, 28)
		model.updateConsole(consoleKey(":"))
		for _, r := range "bogus" {
			model.updateConsole(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
		model.updateConsole(tea.KeyMsg{Type: tea.KeyEnter})
		if model.err != "unknown command: bogus" {
			t.Fatalf("err = %q", model.err)
		}
	})

	t.Run("quit returns a command", func(t *testing.T) {
		model := newConsoleTestModel(tabConnection, 100, 28)
		model.updateConsole(consoleKey(":"))
		model.updateConsole(consoleKey("q"))
		cmd, _ := model.updateConsole(tea.KeyMsg{Type: tea.KeyEnter})
		if cmd == nil {
			t.Fatal(":q did not return a quit command")
		}
	})

	t.Run("help and namespace overlays", func(t *testing.T) {
		model := newConsoleTestModel(tabConnection, 100, 28)
		model.updateConsole(consoleKey(":"))
		for _, r := range "help" {
			model.updateConsole(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
		model.updateConsole(tea.KeyMsg{Type: tea.KeyEnter})
		if model.console.overlay != overlayHelp {
			t.Fatalf("overlay = %d, want help", model.console.overlay)
		}
		model.closeConsoleOverlay()

		model.dataPlaneStatus = clientdataplane.Status{}
		model.namespaces = []clientremote.Namespace{{Name: "default"}}
		model.updateConsole(consoleKey(":"))
		model.updateConsole(consoleKey("n"))
		model.updateConsole(consoleKey("s"))
		model.updateConsole(tea.KeyMsg{Type: tea.KeyEnter})
		if model.console.overlay != overlayNamespace {
			t.Fatalf("overlay = %d, want namespace", model.console.overlay)
		}
	})

	t.Run("namespace command supports connected switching", func(t *testing.T) {
		model := newConsoleTestModel(tabConnection, 100, 28)
		model.namespaces = []clientremote.Namespace{{Name: "default"}}
		model.updateConsole(consoleKey(":"))
		model.updateConsole(consoleKey("n"))
		model.updateConsole(consoleKey("s"))
		model.updateConsole(tea.KeyMsg{Type: tea.KeyEnter})
		if model.console.overlay != overlayNamespace || model.err != "" {
			t.Fatalf("overlay=%d err=%q, want namespace selection", model.console.overlay, model.err)
		}
	})

	t.Run("command bar stays closed while typing a form", func(t *testing.T) {
		model := newConsoleTestModel(tabConnection, 100, 28)
		model.loginAdding = true
		if _, handled := model.updateConsole(consoleKey(":")); handled {
			t.Fatal(": should not open the command bar while the profile form is open")
		}
		model.loginAdding = false
		model.actionMode = actionExec
		if _, handled := model.updateConsole(consoleKey(":")); handled {
			t.Fatal(": should not open the command bar during an action")
		}
	})
}

func TestUpdateConsoleFilterMode(t *testing.T) {
	podsModel := func() Model {
		model := newConsoleTestModel(tabWorkloads, 120, 32)
		ports := []clientremote.PodPort{{Name: "http", Port: 8080, Protocol: "TCP"}}
		model.pods = []clientremote.Pod{
			{Name: "api-0", Phase: "Running", Ports: ports},
			{Name: "web-0", Phase: "Pending", Ports: ports},
			{Name: "cache-0", Phase: "Running", Ports: ports},
		}
		return model
	}

	t.Run("typing filters rows live", func(t *testing.T) {
		model := podsModel()
		if _, handled := model.updateConsole(consoleKey("/")); !handled {
			t.Fatal("/ was not handled")
		}
		if model.console.inputMode != inputFilter {
			t.Fatalf("inputMode = %d, want filter", model.console.inputMode)
		}
		model.updateConsole(consoleKey("web"))
		if model.consoleItemCount() != 1 {
			t.Fatalf("item count = %d, want 1", model.consoleItemCount())
		}
		assertConsoleContains(t, model.View(), "/web", "web-0")
		model.updateConsole(tea.KeyMsg{Type: tea.KeyEnter})
		if model.console.inputMode != inputNone || model.consoleItemCount() != 1 {
			t.Fatalf(
				"inputMode=%d count=%d after enter, filter should persist",
				model.console.inputMode,
				model.consoleItemCount(),
			)
		}
	})

	t.Run("selection maps to original index", func(t *testing.T) {
		model := podsModel()
		model.updateConsole(consoleKey("/"))
		model.updateConsole(consoleKey("cache"))
		row, ok := model.selectedConsoleRow()
		if !ok || row.index != 2 {
			t.Fatalf("selected row ok=%v index=%d, want index 2", ok, row.index)
		}
		next, _ := model.updatePods(consoleKey("f"))
		updated := next.(Model)
		if updated.actionMode != actionPortForward || updated.actionPod != "cache-0" {
			t.Fatalf("action=%d pod=%q, want port forward on cache-0", updated.actionMode, updated.actionPod)
		}
	})

	t.Run("esc in input clears filter", func(t *testing.T) {
		model := podsModel()
		model.updateConsole(consoleKey("/"))
		model.updateConsole(consoleKey("web"))
		model.updateConsole(tea.KeyMsg{Type: tea.KeyEsc})
		if model.consoleItemCount() != 3 || model.console.filters[tabWorkloads] != "" {
			t.Fatalf("count=%d filter=%q after esc", model.consoleItemCount(), model.console.filters[tabWorkloads])
		}
	})

	t.Run("esc outside input clears applied filter", func(t *testing.T) {
		model := podsModel()
		model.updateConsole(consoleKey("/"))
		model.updateConsole(consoleKey("web"))
		model.updateConsole(tea.KeyMsg{Type: tea.KeyEnter})
		model.updateConsole(tea.KeyMsg{Type: tea.KeyEsc})
		if model.consoleItemCount() != 3 {
			t.Fatalf("count = %d, want filter cleared", model.consoleItemCount())
		}
	})

	t.Run("filters are per view", func(t *testing.T) {
		model := podsModel()
		model.services = []clientremote.Service{{Name: "api"}, {Name: "web"}}
		model.updateConsole(consoleKey("/"))
		model.updateConsole(consoleKey("web"))
		model.updateConsole(tea.KeyMsg{Type: tea.KeyEnter})
		model.updateConsole(tea.KeyMsg{Type: tea.KeyTab})
		if model.activeTab != tabServices || model.consoleItemCount() != 2 {
			t.Fatalf("services tab count=%d activeTab=%d, want unfiltered", model.consoleItemCount(), model.activeTab)
		}
	})

	t.Run("filter matches status and meta", func(t *testing.T) {
		model := podsModel()
		model.updateConsole(consoleKey("/"))
		model.updateConsole(consoleKey("pending"))
		rows := model.consoleWorkloadRows()
		if len(rows) != 1 || rows[0].title != "web-0" {
			t.Fatalf("rows = %v, want only web-0 by status match", rows)
		}
	})

	t.Run("filter is unavailable on connection view", func(t *testing.T) {
		model := newConsoleTestModel(tabConnection, 100, 28)
		if _, handled := model.updateConsole(consoleKey("/")); handled {
			t.Fatal("/ should not open the filter bar on the connection view")
		}
	})
}

func newConsoleTestModel(activeTab tab, width, height int) Model {
	return Model{
		width:     width,
		height:    height,
		mode:      viewMain,
		activeTab: activeTab,
		activeProfile: clientprofile.Profile{
			ID:          "test",
			DisplayName: "test-server",
			BaseURL:     "https://console.example.test",
		},
		authSession:     AuthSession{Authenticated: true, UserName: "operator"},
		namespace:       "default",
		dataPlaneStatus: clientdataplane.Status{State: "connected"},
		selectedMode:    clientdataplane.ModeSOCKS,
	}
}

func consoleKey(value string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(value)}
}

func assertConsoleContains(t *testing.T, value string, expected ...string) {
	t.Helper()
	for _, item := range expected {
		if !strings.Contains(value, item) {
			t.Errorf("rendered view does not contain %q", item)
		}
	}
}
