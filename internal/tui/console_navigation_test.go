package tui

import (
	"testing"

	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
)

func TestUpdateConsoleMovementKeys(t *testing.T) {
	tests := []struct {
		name       string
		key        string
		start      int
		wantCursor int
	}{
		{name: "vim down", key: "j", start: 5, wantCursor: 6},
		{name: "arrow down", key: keyDown, start: 5, wantCursor: 6},
		{name: "vim up", key: "k", start: 5, wantCursor: 4},
		{name: "arrow up", key: "up", start: 5, wantCursor: 4},
		{name: "page down", key: "pgdown", start: 1, wantCursor: 13},
		{name: "page up", key: "pgup", start: 15, wantCursor: 3},
		{name: "home", key: "home", start: 10, wantCursor: 0},
		{name: "vim home", key: "g", start: 10, wantCursor: 0},
		{name: "end", key: "end", start: 5, wantCursor: 19},
		{name: "vim end", key: "G", start: 5, wantCursor: 19},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := newConsoleTestModel(tabWorkloads, 100, 24)
			model.pods = make([]clientremote.Pod, 20)
			model.setConsoleCursor(test.start)
			if _, handled := model.updateConsoleMovementKey(consoleKey(test.key)); !handled {
				t.Fatalf("key %q was not handled", test.key)
			}
			if model.cursor != test.wantCursor {
				t.Fatalf("cursor = %d, want %d", model.cursor, test.wantCursor)
			}
		})
	}

	connection := newConsoleTestModel(tabConnection, 100, 24)
	if _, handled := connection.updateConsoleMovementKey(consoleKey("j")); handled {
		t.Fatal("connection view handled a list movement key")
	}
	login := newConsoleTestModel(tabWorkloads, 100, 24)
	login.mode = viewLogin
	if _, handled := login.updateConsoleMovementKey(consoleKey("j")); handled {
		t.Fatal("login view handled a list movement key")
	}
}

func TestRunConsoleCommandStateGuards(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		model := newConsoleTestModel(tabConnection, 100, 24)
		if cmd := model.runConsoleCommand("   "); cmd != nil || model.err != "" {
			t.Fatalf("cmd=%v err=%q", cmd != nil, model.err)
		}
	})

	t.Run("namespace requires login and inventory", func(t *testing.T) {
		model := newConsoleTestModel(tabConnection, 100, 24)
		model.mode = viewLogin
		model.runConsoleCommand(commandNamespace)
		if model.err != "log in to select a namespace" {
			t.Fatalf("err = %q", model.err)
		}
		model.mode = viewMain
		model.runConsoleCommand(commandNamespace)
		if model.err != "no namespaces loaded" {
			t.Fatalf("err = %q", model.err)
		}
	})

	t.Run("server manager guards active state", func(t *testing.T) {
		model := newConsoleTestModel(tabConnection, 100, 24)
		model.mode = viewLogin
		model.runConsoleCommand(string(resourceProfiles))
		if model.err != "already managing servers" {
			t.Fatalf("err = %q", model.err)
		}
		model.mode = viewMain
		model.runConsoleCommand(string(resourceProfiles))
		if model.err != "disconnect before changing server" {
			t.Fatalf("err = %q", model.err)
		}
		model.dataPlaneStatus.State = ""
		cmd := model.runConsoleCommand(string(resourceProfiles))
		if cmd == nil || model.console.overlay != overlayProfiles {
			t.Fatalf("cmd=%v overlay=%d", cmd != nil, model.console.overlay)
		}
	})

	t.Run("tab requires authentication", func(t *testing.T) {
		model := newConsoleTestModel(tabConnection, 100, 24)
		model.authSession.Authenticated = false
		if cmd := model.gotoConsoleTab(tabWorkloads); cmd != nil || model.err == "" {
			t.Fatalf("cmd=%v err=%q", cmd != nil, model.err)
		}
		model.authSession.Authenticated = true
		if cmd := model.gotoConsoleTab(tabConnection); cmd != nil {
			t.Fatal("active tab returned a loading command")
		}
	})
}

func TestBeginNamespaceSwitchStates(t *testing.T) {
	unchanged := newConsoleTestModel(tabConnection, 100, 24)
	if cmd := unchanged.beginNamespaceSwitch("default"); cmd != nil || unchanged.status != "Namespace unchanged" {
		t.Fatalf("cmd=%v status=%q", cmd != nil, unchanged.status)
	}

	disconnected := newConsoleTestModel(tabConnection, 100, 24)
	disconnected.dataPlaneStatus.State = ""
	disconnected.workspace.resource = resourceConnection
	cmd := disconnected.beginNamespaceSwitch("all")
	if cmd == nil {
		t.Fatal("disconnected namespace switch returned nil command")
	}
	message, ok := cmd().(namespaceChangedMsg)
	if !ok || message.namespace != "" || message.resource != resourcePods {
		t.Fatalf("message=%#v", message)
	}

	connected := newConsoleTestModel(tabConnection, 100, 24)
	if cmd := connected.beginNamespaceSwitch("team-a"); cmd == nil || !connected.pendingNamespaceSet ||
		connected.pendingNamespace != "team-a" || connected.status != "Switching to team-a..." {
		t.Fatalf(
			"cmd=%v pending=%q set=%v status=%q",
			cmd != nil,
			connected.pendingNamespace,
			connected.pendingNamespaceSet,
			connected.status,
		)
	}
}

func TestConsoleSelectionAndGeometry(t *testing.T) {
	model := newConsoleTestModel(tabWorkloads, 120, 32)
	model.pods = []clientremote.Pod{{Name: "api-0"}}
	row, ok := model.selectedConsoleRow()
	if !ok || row.title != "api-0" {
		t.Fatalf("row=%#v ok=%v", row, ok)
	}
	model.cursor = 2
	if _, ok := model.selectedConsoleRow(); ok {
		t.Fatal("out-of-range cursor returned a row")
	}
	model.activeTab = tabConnection
	if _, ok := model.selectedConsoleRow(); ok {
		t.Fatal("connection tab returned a row")
	}

	tests := []struct {
		name        string
		width       int
		wantRowY    int
		wantStride  int
		wantDetailX int
	}{
		{name: "narrow", width: 70, wantRowY: 8, wantStride: 2, wantDetailX: 70},
		{name: "standard", width: 90, wantRowY: 8, wantStride: 1, wantDetailX: 52},
		{name: "wide", width: 120, wantRowY: 7, wantStride: 2, wantDetailX: 80},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := newConsoleTestModel(tabWorkloads, test.width, 32)
			rowY, stride := model.consoleListGeometry()
			if rowY != test.wantRowY || stride != test.wantStride ||
				model.consoleDetailStartX() != test.wantDetailX {
				t.Fatalf(
					"rowY=%d stride=%d detailX=%d",
					rowY,
					stride,
					model.consoleDetailStartX(),
				)
			}
		})
	}
}
