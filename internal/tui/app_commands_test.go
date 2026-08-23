package tui

import (
	"strings"
	"testing"

	clientdataplane "github.com/fengqi-dev/kube-loop/internal/client/dataplane"
)

func TestHintTextMatchesActiveContext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		edit func(*Model)
		want string
	}{
		{name: "login", edit: func(model *Model) { model.mode = viewLogin }, want: "server"},
		{name: "action", edit: func(model *Model) { model.actionMode = actionExec }, want: "confirm"},
		{name: "connection", edit: func(model *Model) { model.activeTab = tabConnection }, want: "connect"},
		{name: "workloads", edit: func(model *Model) { model.activeTab = tabWorkloads }, want: "forward"},
		{name: "services", edit: func(model *Model) { model.activeTab = tabServices }, want: "exchange"},
		{name: "tasks", edit: func(model *Model) { model.activeTab = tabTasks }, want: "stop"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			model := Model{mode: viewMain}
			test.edit(&model)
			if hint := model.hintText(); !strings.Contains(hint, test.want) {
				t.Fatalf("hintText() = %q, want %q", hint, test.want)
			}
		})
	}
}

func TestConnectedRecognizesRuntimeStates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		state string
		want  bool
	}{
		{state: dataPlaneStateDisconnected},
		{state: dataPlaneStateConnected, want: true},
		{state: "active", want: true},
		{state: "reconnecting", want: true},
	}
	for _, test := range tests {
		t.Run(test.state, func(t *testing.T) {
			t.Parallel()

			model := Model{dataPlaneStatus: clientdataplane.Status{State: test.state}}
			if got := model.connected(); got != test.want {
				t.Fatalf("connected() = %t, want %t", got, test.want)
			}
		})
	}
}
