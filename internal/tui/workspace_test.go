package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	clientdataplane "github.com/fengqi-dev/kube-loop/internal/client/dataplane"
	clientprofile "github.com/fengqi-dev/kube-loop/internal/client/profile"
	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
)

func newWorkspaceTestModel(resource workspaceResource) Model {
	model := newConsoleTestModel(tabConnection, 120, 32)
	model.workspace = workspaceState{
		initialized: true,
		resource:    resource,
		views:       map[workspaceResource]workspaceViewState{},
		history:     []workspaceResource{resource},
		config:      workspaceConfig{Version: 1, Aliases: map[string]string{}, Hotkeys: map[string]string{}},
	}
	model.mode = viewMain
	model.selectedMode = clientdataplane.ModeTUN
	model.dataPlaneStatus = clientdataplane.Status{State: "connected", Mode: "tun"}
	model.pods = []clientremote.Pod{
		{Name: "api-0", Namespace: "default", Phase: "Running", PodIP: "10.0.0.1", NodeName: "worker-a", Ready: true},
		{Name: "worker-0", Namespace: "default", Phase: "Pending", NodeName: "worker-b"},
	}
	model.services = []clientremote.Service{{Name: "api", Namespace: "default", Type: "ClusterIP", ClusterIP: "10.96.0.1"}}
	model.namespaces = []clientremote.Namespace{{Name: "default"}, {Name: "production"}}
	model.profiles = clientprofile.State{ActiveProfileID: "test", Profiles: []clientprofile.Profile{model.activeProfile}}
	return model
}

func TestWorkspaceLayoutUsesSingleResourceTable(t *testing.T) {
	model := newWorkspaceTestModel(resourcePods)
	view := model.View()
	for _, expected := range []string{"Cluster:", "Namespace:", "default", "Mode:", "TUN", "Pods(default)[2]", "api-0", "POD IP", "<pods>"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("workspace view missing %q", expected)
		}
	}
	if !strings.Contains(view, "<0>") || !strings.Contains(view, "<n>") || strings.Contains(view, "RESOURCE WORKSPACE") {
		t.Fatal("K9s-style workspace header is not rendered")
	}
}

func TestWorkspaceSelectedRowDoesNotWrapHyphenatedName(t *testing.T) {
	model := newWorkspaceTestModel(resourcePods)
	model.width = 76
	model.pods[0].Name = "coredns-589f44dc88-h7bcn"
	view := model.View()
	if !strings.Contains(view, "coredns-589f44dc88-h7bcn") || strings.Contains(view, "coredns-589f44dc88-\nh7bcn") {
		t.Fatalf("selected pod name wrapped: %q", view)
	}
}

func TestWorkspaceCommandCompletionAndHistory(t *testing.T) {
	model := newWorkspaceTestModel(resourceConnection)
	model.updateWorkspace(workspaceKey(":"))
	model.updateWorkspace(workspaceKey("p"))
	model.updateWorkspace(tea.KeyMsg{Type: tea.KeyTab})
	if !strings.HasPrefix(model.workspace.inputText, "p") {
		t.Fatalf("completion = %q", model.workspace.inputText)
	}
	model.workspace.inputText = "pods"
	cmd := model.updateWorkspaceInput(tea.KeyMsg{Type: tea.KeyEnter})
	if model.workspace.resource != resourcePods || cmd == nil {
		t.Fatalf("resource=%q cmd=%v", model.workspace.resource, cmd)
	}
	model.runWorkspaceCommand("services")
	model.workspaceHistory(-1)
	if model.workspace.resource != resourcePods {
		t.Fatalf("history back resource = %q", model.workspace.resource)
	}
	model.workspaceHistory(1)
	if model.workspace.resource != resourceServices {
		t.Fatalf("history forward resource = %q", model.workspace.resource)
	}
}

func TestWorkspaceConnectionResourceShortcuts(t *testing.T) {
	tests := []struct {
		key      string
		resource workspaceResource
	}{
		{key: "p", resource: resourcePods},
		{key: "v", resource: resourceServices},
		{key: "n", resource: resourceNamespaces},
		{key: "s", resource: resourceTasks},
	}
	for _, test := range tests {
		model := newWorkspaceTestModel(resourceConnection)
		cmd, handled := model.updateWorkspace(workspaceKey(test.key))
		if !handled || cmd == nil || model.workspace.resource != test.resource {
			t.Fatalf("key %q: handled=%v cmd=%v resource=%q", test.key, handled, cmd, model.workspace.resource)
		}
	}

	view := newWorkspaceTestModel(resourceConnection).View()
	for _, expected := range []string{"<p> Pods", "<v> Services", "<n> Namespaces", "<s> Sessions"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("connection view missing %q", expected)
		}
	}
}

func TestWorkspaceRegexInverseAndFuzzyFilters(t *testing.T) {
	model := newWorkspaceTestModel(resourcePods)
	view := model.workspaceView()
	view.filter = "^api"
	model.setWorkspaceView(view)
	if rows := model.workspaceFilteredRows(); len(rows) != 1 || rows[0].title != "api-0" {
		t.Fatalf("regex rows = %#v", rows)
	}
	view.filter = "!running"
	model.setWorkspaceView(view)
	if rows := model.workspaceFilteredRows(); len(rows) != 1 || rows[0].title != "worker-0" {
		t.Fatalf("inverse rows = %#v", rows)
	}
	view.filter = "-f wrk0"
	model.setWorkspaceView(view)
	if rows := model.workspaceFilteredRows(); len(rows) != 1 || rows[0].title != "worker-0" {
		t.Fatalf("fuzzy rows = %#v", rows)
	}
}

func TestWorkspaceDetailAndResourceActions(t *testing.T) {
	model := newWorkspaceTestModel(resourcePods)
	model.updateWorkspace(tea.KeyMsg{Type: tea.KeyEnter})
	if !model.workspaceView().detail || !strings.Contains(model.View(), "Pods / api-0") {
		t.Fatal("enter did not open pod detail")
	}
	model.updateWorkspace(tea.KeyMsg{Type: tea.KeyEsc})
	if model.workspaceView().detail {
		t.Fatal("escape did not leave detail")
	}
	model.pods[0].Ports = []clientremote.PodPort{{Name: "http", Port: 8080, Protocol: "TCP"}}
	model.updateWorkspace(workspaceKey("f"))
	if model.actionMode != actionPortForward || model.actionPod != "api-0" {
		t.Fatalf("action=%d pod=%q", model.actionMode, model.actionPod)
	}
	view := model.View()
	if !strings.Contains(view, "PORT FORWARD") || strings.Contains(view, "OPERATIONS CONSOLE") {
		t.Fatalf("action did not stay inside workspace: %q", view)
	}
}

func TestWorkspaceIgnoresStaleResourceLoads(t *testing.T) {
	model := newWorkspaceTestModel(resourceServices)
	model.workspace.loadGeneration = 2
	updated, _ := model.Update(workspaceLoadedMsg{
		resource:   resourcePods,
		generation: 1,
		message:    podsLoadedMsg{pods: []clientremote.Pod{{Name: "stale"}}},
	})
	if pods := updated.(Model).pods; len(pods) != 2 || pods[0].Name == "stale" {
		t.Fatalf("stale pods load was applied: %#v", pods)
	}
}

func TestWorkspaceConfigValidation(t *testing.T) {
	config := workspaceConfig{
		Version: 1,
		Aliases: map[string]string{"pp": "pods", "q": "services", "bad": "deployments"},
		Hotkeys: map[string]string{"ctrl+p": "servers", "/": "sessions", "ctrl+x": "deployments"},
	}
	warnings := validateWorkspaceConfig(&config)
	if config.Aliases["pp"] != "pods" || config.Hotkeys["ctrl+p"] != "servers" {
		t.Fatal("valid config entries were removed")
	}
	if _, ok := config.Aliases["q"]; ok {
		t.Fatal("reserved alias was retained")
	}
	if _, ok := config.Hotkeys["/"]; ok {
		t.Fatal("reserved hotkey was retained")
	}
	if len(warnings) < 4 {
		t.Fatalf("warnings = %v", warnings)
	}
}

func TestWorkspaceConfigLoadsFromHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	directory := filepath.Join(home, ".kubeloop")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	content := "version: 1\naliases:\n  pp: pods\nhotkeys:\n  ctrl+p: servers\n"
	if err := os.WriteFile(filepath.Join(directory, "tui.yaml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	config, warning := loadWorkspaceConfig()
	if warning != "" || config.Aliases["pp"] != "pods" || config.Hotkeys["ctrl+p"] != "servers" {
		t.Fatalf("config=%#v warning=%q", config, warning)
	}
}

func workspaceKey(value string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(value)}
}

func TestWorkspaceLogoutRequiresDisconnectedSession(t *testing.T) {
	model := newWorkspaceTestModel(resourceConnection)
	model.authSession.Authenticated = true
	cmd, handled := model.updateWorkspace(workspaceKey("L"))
	if !handled || cmd != nil || model.err != "disconnect before logging out" {
		t.Fatalf("connected logout: handled=%v cmd=%v err=%q", handled, cmd, model.err)
	}

	model = newWorkspaceTestModel(resourceConnection)
	model.authSession.Authenticated = true
	model.dataPlaneStatus.State = "disconnected"
	cmd, handled = model.updateWorkspace(workspaceKey("L"))
	if !handled || cmd == nil || !model.loading || model.status != "Logging out..." {
		t.Fatalf("disconnected logout: handled=%v cmd=%v loading=%v status=%q", handled, cmd, model.loading, model.status)
	}

	model.loading = false
	model.status = ""
	if cmd = model.runWorkspaceCommand("logout"); cmd == nil || !model.loading {
		t.Fatalf("logout command: cmd=%v loading=%v", cmd, model.loading)
	}
}

func TestWorkspaceConnectCommandIsIdempotent(t *testing.T) {
	model := newWorkspaceTestModel(resourceConnection)
	model.authSession.Authenticated = true
	model.dataPlaneStatus.State = "disconnected"
	cmd := model.runWorkspaceCommand("connect")
	if cmd == nil || !model.loading || model.status != "[1/3] Creating Cluster Session..." {
		t.Fatalf("connect command: cmd=%v loading=%v status=%q err=%q", cmd, model.loading, model.status, model.err)
	}

	model.loading = false
	model.dataPlaneStatus.State = "connected"
	cmd = model.runWorkspaceCommand("connect")
	if cmd != nil || model.loading || model.status != "Already connected" {
		t.Fatalf("connected command: cmd=%v loading=%v status=%q", cmd, model.loading, model.status)
	}
}

func TestWorkspacePodAndServiceTablesShowNamespace(t *testing.T) {
	for _, resource := range []workspaceResource{resourcePods, resourceServices} {
		model := newWorkspaceTestModel(resource)
		rows := model.workspaceRawRows()
		if len(rows) == 0 {
			t.Fatalf("%s rows are empty", resource)
		}
		if !strings.Contains(model.workspaceTableHeader(), "NAMESPACE") ||
			!strings.Contains(model.workspaceTableRow(rows[0]), "default") {
			t.Fatalf("%s table does not show namespace", resource)
		}
	}
}

func TestWorkspaceNamespaceShortcutReconnectsConnectedDataPlane(t *testing.T) {
	model := newWorkspaceTestModel(resourcePods)
	cmd, handled := model.updateWorkspace(workspaceKey("n"))
	if !handled || cmd != nil || model.console.overlay != overlayNamespace {
		t.Fatalf("namespace shortcut: handled=%v cmd=%v overlay=%v", handled, cmd, model.console.overlay)
	}

	model.console.query = "prod"
	model.console.overlayPos = 0
	cmd = model.updateConsoleOverlay(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || model.pendingNamespace != "production" || !model.loading {
		t.Fatalf("namespace switch: cmd=%v pending=%q loading=%v", cmd, model.pendingNamespace, model.loading)
	}
}
