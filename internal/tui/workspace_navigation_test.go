package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestUpdateWorkspaceModeKeys(t *testing.T) {
	t.Run("all namespaces", func(t *testing.T) {
		model := newWorkspaceTestModel(resourcePods)
		cmd, handled := model.updateWorkspaceModeKey(workspaceKey("0"))
		if !handled || cmd == nil || !model.pendingNamespaceSet || model.pendingNamespace != "" {
			t.Fatalf(
				"handled=%v cmd=%v pending=%q set=%v",
				handled,
				cmd != nil,
				model.pendingNamespace,
				model.pendingNamespaceSet,
			)
		}
	})

	t.Run("last namespace", func(t *testing.T) {
		model := newWorkspaceTestModel(resourceServices)
		model.activeProfile.LastNamespace = "production"
		cmd, handled := model.updateWorkspaceModeKey(workspaceKey("1"))
		if !handled || cmd == nil || model.pendingNamespace != "production" {
			t.Fatalf("handled=%v cmd=%v pending=%q", handled, cmd != nil, model.pendingNamespace)
		}
	})

	t.Run("no namespace", func(t *testing.T) {
		model := newWorkspaceTestModel(resourcePods)
		model.namespaces = nil
		model.activeProfile.LastNamespace = ""
		if cmd, handled := model.updateWorkspaceModeKey(workspaceKey("1")); !handled || cmd != nil ||
			model.err != "no namespace available" {
			t.Fatalf("handled=%v cmd=%v err=%q", handled, cmd != nil, model.err)
		}
	})

	t.Run("command and filters", func(t *testing.T) {
		model := newWorkspaceTestModel(resourceConnection)
		if _, handled := model.updateWorkspaceModeKey(workspaceKey(":")); !handled ||
			model.workspace.input != workspaceInputCommand {
			t.Fatalf("command handled=%v input=%d", handled, model.workspace.input)
		}
		model.workspace.input = workspaceInputNone
		if _, handled := model.updateWorkspaceModeKey(workspaceKey("/")); !handled || model.status == "" {
			t.Fatalf("connection filter handled=%v status=%q", handled, model.status)
		}
		model.workspace.resource = resourcePods
		if _, handled := model.updateWorkspaceModeKey(workspaceKey("/")); !handled ||
			model.workspace.input != workspaceInputFilter {
			t.Fatalf("filter handled=%v input=%d", handled, model.workspace.input)
		}
	})

	t.Run("namespace and help", func(t *testing.T) {
		model := newWorkspaceTestModel(resourcePods)
		if _, handled := model.updateWorkspaceModeKey(workspaceKey("n")); !handled ||
			model.console.overlay != overlayNamespace {
			t.Fatalf("namespace handled=%v overlay=%d", handled, model.console.overlay)
		}
		model.console.overlay = overlayNone
		if _, handled := model.updateWorkspaceModeKey(workspaceKey("?")); !handled || !model.workspace.help {
			t.Fatalf("help handled=%v open=%v", handled, model.workspace.help)
		}
		if _, handled := model.updateWorkspaceModeKey(workspaceKey("z")); handled {
			t.Fatal("unknown mode key was handled")
		}
	})
}

func TestUpdateWorkspaceNavigationKeys(t *testing.T) {
	tests := []struct {
		name       string
		key        string
		start      int
		wantCursor int
	}{
		{name: "down", key: "j", start: 0, wantCursor: 1},
		{name: "up", key: "k", start: 1, wantCursor: 0},
		{name: "page down", key: "pgdown", start: 0, wantCursor: 1},
		{name: "page up", key: "pgup", start: 1, wantCursor: 0},
		{name: "home", key: "home", start: 1, wantCursor: 0},
		{name: "end", key: "end", start: 0, wantCursor: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := newWorkspaceTestModel(resourcePods)
			model.setWorkspaceCursor(test.start)
			if _, handled := model.updateWorkspaceNavigationKey(workspaceKey(test.key)); !handled {
				t.Fatalf("key %q was not handled", test.key)
			}
			if cursor := model.workspaceView().cursor; cursor != test.wantCursor {
				t.Fatalf("cursor=%d, want %d", cursor, test.wantCursor)
			}
		})
	}

	detail := newWorkspaceTestModel(resourcePods)
	if _, handled := detail.updateWorkspaceNavigationKey(tea.KeyMsg{Type: tea.KeyEnter}); !handled ||
		!detail.workspaceView().detail {
		t.Fatalf("detail handled=%v open=%v", handled, detail.workspaceView().detail)
	}
	if _, handled := detail.updateWorkspaceNavigationKey(tea.KeyMsg{Type: tea.KeyEsc}); !handled ||
		detail.workspaceView().detail {
		t.Fatalf("escape handled=%v open=%v", handled, detail.workspaceView().detail)
	}

	refresh := newWorkspaceTestModel(resourcePods)
	if cmd, handled := refresh.updateWorkspaceNavigationKey(workspaceKey("r")); !handled || cmd == nil ||
		!refresh.loading {
		t.Fatalf("refresh handled=%v cmd=%v loading=%v", handled, cmd != nil, refresh.loading)
	}
	quit := newWorkspaceTestModel(resourceConnection)
	if cmd, handled := quit.updateWorkspaceNavigationKey(workspaceKey("q")); !handled || cmd == nil {
		t.Fatalf("quit handled=%v cmd=%v", handled, cmd != nil)
	}
}

func TestRunWorkspaceCommandGuards(t *testing.T) {
	model := newWorkspaceTestModel(resourceConnection)
	if cmd := model.runWorkspaceCommand("  "); cmd != nil {
		t.Fatal("empty command returned a command")
	}
	if cmd := model.runWorkspaceCommand("help"); cmd != nil || !model.workspace.help {
		t.Fatalf("help cmd=%v open=%v", cmd != nil, model.workspace.help)
	}
	model.workspace.help = false
	if cmd := model.runWorkspaceCommand("unknown"); cmd != nil || model.err != "unknown command: unknown" {
		t.Fatalf("unknown cmd=%v err=%q", cmd != nil, model.err)
	}
	model.err = ""
	if cmd := model.runWorkspaceCommand("pods missing"); cmd != nil || model.err != "unknown namespace: missing" {
		t.Fatalf("namespace cmd=%v err=%q", cmd != nil, model.err)
	}
	if cmd := model.runWorkspaceCommand("services default"); cmd == nil ||
		model.workspace.resource != resourceServices {
		t.Fatalf("resource cmd=%v resource=%q", cmd != nil, model.workspace.resource)
	}
}

func TestUpdateWorkspaceInputContracts(t *testing.T) {
	t.Run("filter edit and cancel", func(t *testing.T) {
		model := newWorkspaceTestModel(resourcePods)
		view := model.workspaceView()
		view.filter = "api"
		model.setWorkspaceView(view)
		model.workspace.input = workspaceInputFilter
		model.workspace.inputBefore = "api"
		model.workspace.inputText = "["
		model.updateWorkspaceInput(workspaceKey("["))
		if model.err == "" || model.workspaceView().filter != "[[" {
			t.Fatalf("err=%q filter=%q", model.err, model.workspaceView().filter)
		}
		model.updateWorkspaceInput(tea.KeyMsg{Type: tea.KeyBackspace})
		model.updateWorkspaceInput(tea.KeyMsg{Type: tea.KeyBackspace})
		if model.err != "" || model.workspaceView().filter != "" {
			t.Fatalf("err=%q filter=%q", model.err, model.workspaceView().filter)
		}
		model.updateWorkspaceInput(tea.KeyMsg{Type: tea.KeyEsc})
		if model.workspace.input != workspaceInputNone || model.workspaceView().filter != "api" {
			t.Fatalf("input=%d filter=%q", model.workspace.input, model.workspaceView().filter)
		}
	})

	t.Run("command history and completion", func(t *testing.T) {
		model := newWorkspaceTestModel(resourceConnection)
		model.workspace.input = workspaceInputCommand
		model.workspace.commands = []string{"pods", "services"}
		model.workspace.commandPos = -1
		model.updateWorkspaceInput(workspaceKey("up"))
		if model.workspace.inputText != "services" {
			t.Fatalf("first history entry=%q", model.workspace.inputText)
		}
		model.updateWorkspaceInput(workspaceKey("up"))
		if model.workspace.inputText != "pods" {
			t.Fatalf("second history entry=%q", model.workspace.inputText)
		}
		model.updateWorkspaceInput(workspaceKey(keyDown))
		if model.workspace.inputText != "services" {
			t.Fatalf("forward history entry=%q", model.workspace.inputText)
		}
		model.workspace.inputText = "p"
		model.workspace.suggestion = 0
		model.updateWorkspaceInput(tea.KeyMsg{Type: tea.KeyShiftTab})
		if model.workspace.inputText == "" {
			t.Fatal("shift-tab completion returned empty input")
		}
	})

	t.Run("command enter and ignored navigation", func(t *testing.T) {
		model := newWorkspaceTestModel(resourceConnection)
		model.workspace.input = workspaceInputCommand
		model.workspace.inputText = "help"
		if cmd := model.updateWorkspaceInput(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil ||
			!model.workspace.help || model.workspace.input != workspaceInputNone {
			t.Fatalf("cmd=%v help=%v input=%d", cmd != nil, model.workspace.help, model.workspace.input)
		}
		model.workspace.input = workspaceInputCommand
		model.workspace.inputText = "pods"
		if cmd := model.updateWorkspaceInput(tea.KeyMsg{Type: tea.KeyLeft}); cmd != nil ||
			model.workspace.inputText != "pods" {
			t.Fatalf("cmd=%v input=%q", cmd != nil, model.workspace.inputText)
		}
	})
}
