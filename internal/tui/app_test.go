package tui

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	clientdataplane "github.com/fengqi-dev/kube-loop/internal/client/dataplane"
	clientprofile "github.com/fengqi-dev/kube-loop/internal/client/profile"
	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
)

func TestTUIMessageErrorExtractsNestedErrors(t *testing.T) {
	want := errors.New("fixture failure")
	tests := []struct {
		name    string
		message tea.Msg
	}{
		{name: "workspace", message: workspaceLoadedMsg{message: authStatusMsg{err: want}}},
		{name: "auth status", message: authStatusMsg{err: want}},
		{name: "login", message: loginResultMsg{err: want}},
		{name: "logout", message: logoutResultMsg{err: want}},
		{name: "namespaces", message: namespacesLoadedMsg{err: want}},
		{name: "pods", message: podsLoadedMsg{err: want}},
		{name: "services", message: servicesLoadedMsg{err: want}},
		{name: "connected", message: dataPlaneConnectedMsg{err: want}},
		{name: "disconnected", message: dataPlaneDisconnectedMsg{err: want}},
		{name: "mode", message: dataPlaneModeMsg{err: want}},
		{name: "profile saved", message: profileSavedMsg{err: want}},
		{name: "profile deleted", message: profileDeletedMsg{err: want}},
		{name: "forward", message: portForwardStartedMsg{err: want}},
		{name: "traffic", message: trafficOperationStartedMsg{err: want}},
		{name: "ssh start", message: podSSHStartedMsg{err: want}},
		{name: "ssh exit", message: podSSHExitedMsg{err: want}},
		{name: "helper uninstall", message: helperServiceUninstalledMsg{err: want}},
		{name: "exec", message: execStartedMsg{err: want}},
		{name: "stop", message: taskStoppedMsg{err: want}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := tuiMessageError(test.message); !errors.Is(got, want) {
				t.Fatalf("tuiMessageError() = %v, want fixture error", got)
			}
		})
	}
	if err := tuiMessageError(struct{}{}); err != nil {
		t.Fatalf("unknown message error = %v", err)
	}
}

func TestUpdateInterfaceMessages(t *testing.T) {
	model := newWorkspaceTestModel(resourceConnection)
	next, _, handled := model.updateInterfaceMessage(tea.WindowSizeMsg{Width: 90, Height: 24})
	model = next.(Model)
	if !handled || model.width != 90 || model.height != 24 {
		t.Fatalf("window update handled=%v size=%dx%d", handled, model.width, model.height)
	}

	next, cmd, handled := model.updateInterfaceMessage(workspaceLoadedMsg{
		resource: resourcePods, generation: 99, message: podsLoadedMsg{},
	})
	if !handled || cmd != nil || len(next.(Model).pods) != len(model.pods) {
		t.Fatalf("stale workspace update handled=%v cmd=%v", handled, cmd != nil)
	}

	model = model.updateClipboardCopied(clipboardCopiedMsg{kind: "ERROR", err: errors.New("denied")})
	if model.err != "copy error: denied" || model.status != "" {
		t.Fatalf("copy error state err=%q status=%q", model.err, model.status)
	}
	model = model.updateClipboardCopied(clipboardCopiedMsg{kind: "EXEC"})
	if model.err != "" || model.status != "EXEC session copied to clipboard" {
		t.Fatalf("copy success state err=%q status=%q", model.err, model.status)
	}

	if _, cmd = model.updateKeyMessage(tea.KeyMsg{Type: tea.KeyCtrlC}); cmd == nil {
		t.Fatal("ctrl+c did not return quit command")
	}
	if _, _, handled = model.updateInterfaceMessage(struct{}{}); handled {
		t.Fatal("unknown interface message was handled")
	}
}

func TestUpdateSessionMessages(t *testing.T) {
	model := newWorkspaceTestModel(resourceConnection)
	profiles := clientprofile.State{
		ActiveProfileID: "active",
		Profiles:        []clientprofile.Profile{{ID: "active", DisplayName: "Active"}},
	}
	next, cmd, handled := model.updateSessionMessage(profilesLoadedMsg{state: profiles})
	model = next.(Model)
	if !handled || cmd == nil || model.activeProfile.ID != "active" {
		t.Fatalf("profiles handled=%v cmd=%v active=%q", handled, cmd != nil, model.activeProfile.ID)
	}

	next, cmd, handled = model.updateSessionMessage(authStatusMsg{err: errors.New("unauthorized")})
	model = next.(Model)
	if !handled || cmd != nil || model.err != "unauthorized" || model.authSession.Authenticated {
		t.Fatalf("auth error handled=%v cmd=%v err=%q", handled, cmd != nil, model.err)
	}

	model.loginCancel = func() {}
	next, cmd, handled = model.updateSessionMessage(loginResultMsg{cancelled: true})
	model = next.(Model)
	if !handled || cmd != nil || model.loginCancel != nil || model.status != "Login cancelled" {
		t.Fatalf("login cancel handled=%v cmd=%v status=%q", handled, cmd != nil, model.status)
	}

	next, _, handled = model.updateSessionMessage(namespacesLoadedMsg{err: errors.New("namespace failure")})
	model = next.(Model)
	if !handled || model.err != "namespace failure" || model.autoConnect {
		t.Fatalf("namespace error handled=%v err=%q autoConnect=%v", handled, model.err, model.autoConnect)
	}

	next, _, handled = model.updateSessionMessage(podsLoadedMsg{pods: []clientremote.Pod{{Name: "api-0"}}})
	model = next.(Model)
	if !handled || len(model.pods) != 1 || model.pods[0].Name != "api-0" {
		t.Fatalf("pods handled=%v pods=%#v", handled, model.pods)
	}
	next, _, handled = model.updateSessionMessage(servicesLoadedMsg{
		services: []clientremote.Service{{Name: "api"}},
	})
	model = next.(Model)
	if !handled || len(model.services) != 1 || model.services[0].Name != "api" {
		t.Fatalf("services handled=%v services=%#v", handled, model.services)
	}

	next, cmd, handled = model.updateSessionMessage(namespaceChangedMsg{namespace: "production"})
	model = next.(Model)
	if !handled || cmd == nil || model.namespace != "production" || model.workspace.resource != resourcePods {
		t.Fatalf(
			"namespace change handled=%v cmd=%v namespace=%q resource=%q",
			handled,
			cmd != nil,
			model.namespace,
			model.workspace.resource,
		)
	}
	if _, _, handled = model.updateSessionMessage(struct{}{}); handled {
		t.Fatal("unknown session message was handled")
	}
}

func TestUpdateDataPlaneMessages(t *testing.T) {
	model := newWorkspaceTestModel(resourceConnection)
	next, _, handled := model.updateDataPlaneMessage(dataPlaneStatusMsg{status: clientdataplane.Status{
		State: "connected", Mode: "socks",
	}})
	model = next.(Model)
	if !handled || model.selectedMode != clientdataplane.ModeSOCKS || model.loading {
		t.Fatalf("status handled=%v mode=%q loading=%v", handled, model.selectedMode, model.loading)
	}

	next, cmd, handled := model.updateDataPlaneMessage(dataPlaneSessionConnectedMsg{
		profile: model.activeProfile,
		session: clientremote.Session{ID: "session"},
		mode:    clientdataplane.ModeSOCKS,
	})
	model = next.(Model)
	if !handled || cmd == nil || model.status != "[2/2] Starting SOCKS Data Plane..." {
		t.Fatalf("session connected handled=%v cmd=%v status=%q", handled, cmd != nil, model.status)
	}

	next, cmd, handled = model.updateDataPlaneMessage(dataPlaneConnectedMsg{
		stage: "Start TUN", err: errors.New("helper failed"),
	})
	model = next.(Model)
	if !handled || cmd != nil || model.err != "Start TUN: helper failed" || model.loading {
		t.Fatalf("connect error handled=%v cmd=%v err=%q", handled, cmd != nil, model.err)
	}

	next, cmd, handled = model.updateDataPlaneMessage(dataPlaneDisconnectedMsg{err: errors.New("disconnect failed")})
	model = next.(Model)
	if !handled || cmd != nil || model.err != "disconnect failed" || model.pendingNamespaceSet {
		t.Fatalf("disconnect error handled=%v cmd=%v err=%q", handled, cmd != nil, model.err)
	}

	next, _, handled = model.updateDataPlaneMessage(dataPlaneModeMsg{
		previous: clientdataplane.ModeTUN, err: errors.New("switch failed"),
	})
	model = next.(Model)
	if !handled || model.selectedMode != clientdataplane.ModeTUN || model.err != "switch failed" {
		t.Fatalf("mode error handled=%v mode=%q err=%q", handled, model.selectedMode, model.err)
	}
	next, _, handled = model.updateDataPlaneMessage(dataPlaneModeMsg{status: clientdataplane.Status{Mode: "socks"}})
	model = next.(Model)
	if !handled || model.status != "Mode switched to socks" {
		t.Fatalf("mode success handled=%v status=%q", handled, model.status)
	}
	if _, _, handled = model.updateDataPlaneMessage(struct{}{}); handled {
		t.Fatal("unknown Data Plane message was handled")
	}
}

func TestUpdateTaskMessages(t *testing.T) {
	model := newWorkspaceTestModel(resourceTasks)
	for _, message := range []tea.Msg{
		portForwardsLoadedMsg{},
		podSSHLoadedMsg{},
		trafficOperationsLoadedMsg{},
	} {
		next, cmd, handled := model.updateTaskListMessage(message)
		model = next.(Model)
		if !handled || cmd != nil {
			t.Fatalf("list message %T handled=%v cmd=%v", message, handled, cmd != nil)
		}
	}

	for _, message := range []tea.Msg{
		profileSavedMsg{err: errors.New("save failed")},
		profileDeletedMsg{err: errors.New("delete failed")},
	} {
		next, cmd, handled := model.updateTaskListMessage(message)
		model = next.(Model)
		if !handled || cmd != nil || model.err == "" {
			t.Fatalf("profile message %T handled=%v cmd=%v err=%q", message, handled, cmd != nil, model.err)
		}
	}

	operations := []tea.Msg{
		portForwardStartedMsg{err: errors.New("forward failed")},
		trafficOperationStartedMsg{err: errors.New("traffic failed")},
		podSSHStartedMsg{err: errors.New("ssh failed")},
		helperServiceUninstalledMsg{err: errors.New("uninstall failed")},
		execStartedMsg{err: errors.New("exec failed")},
		taskStoppedMsg{err: errors.New("stop failed")},
	}
	for _, message := range operations {
		next, cmd, handled := model.updateTaskOperationMessage(message)
		model = next.(Model)
		if !handled || cmd != nil || model.err == "" {
			t.Fatalf("operation %T handled=%v cmd=%v err=%q", message, handled, cmd != nil, model.err)
		}
	}

	next, _, handled := model.updateTaskOperationMessage(helperServiceUninstalledMsg{})
	model = next.(Model)
	if !handled || model.err != "" || model.status != "Helper Service uninstalled" {
		t.Fatalf("helper success handled=%v err=%q status=%q", handled, model.err, model.status)
	}
	model.mode = viewLogin
	if _, cmd, handled := model.updateTaskOperationMessage(tickMsg{}); !handled || cmd == nil {
		t.Fatalf("tick handled=%v cmd=%v", handled, cmd != nil)
	}
	if _, _, handled := model.updateTaskMessage(struct{}{}); handled {
		t.Fatal("unknown task message was handled")
	}
}

func TestModelContextFallsBackToBackground(t *testing.T) {
	model := Model{}
	if model.context() == nil {
		t.Fatal("nil model context")
	}
	model.state = &State{ctx: t.Context()}
	if model.context() != model.state.ctx {
		t.Fatal("model did not return State context")
	}
}
