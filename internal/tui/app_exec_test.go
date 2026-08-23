package tui

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	clientexec "github.com/fengqi-dev/kube-loop/internal/client/exec"
)

func TestWaitExecEvent(t *testing.T) {
	t.Parallel()

	t.Run("event", func(t *testing.T) {
		t.Parallel()

		state := &State{ctx: t.Context(), execEvents: make(chan clientexec.Event, 1)}
		state.execEvents <- clientexec.Event{TaskID: "exec-1", Type: clientexec.EventExit, ExitCode: 7}
		message, ok := waitExecEvent(state)().(execEventMsg)
		if !ok || message.event.TaskID != "exec-1" || message.event.ExitCode != 7 {
			t.Fatalf("waitExecEvent() message = %#v", message)
		}
	})

	t.Run("cancelled", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		state := &State{ctx: ctx, execEvents: make(chan clientexec.Event)}
		if message := waitExecEvent(state)(); message != nil {
			t.Fatalf("cancelled waitExecEvent() = %#v", message)
		}
	})
}

func TestApplyExecEvent(t *testing.T) {
	t.Parallel()

	model := Model{}
	encoded := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", 9000)))
	model.applyExecEvent(clientexec.Event{TaskID: "exec-1", Type: clientexec.EventStdout, Data: encoded})
	if len(model.execTasks) != 1 || model.execTasks[0].State != taskStateRunning ||
		len(model.execTasks[0].Output) != 8192 {
		t.Fatalf("stdout event tasks = %#v", model.execTasks)
	}
	output := model.execTasks[0].Output
	model.applyExecEvent(clientexec.Event{TaskID: "exec-1", Type: clientexec.EventStderr, Data: "invalid"})
	if model.execTasks[0].Output != output {
		t.Fatal("invalid base64 changed exec output")
	}
	model.applyExecEvent(clientexec.Event{TaskID: "exec-1", Type: clientexec.EventExit, ExitCode: 23})
	if model.execTasks[0].State != "exit 23" {
		t.Fatalf("exit state = %q", model.execTasks[0].State)
	}
	model.applyExecEvent(clientexec.Event{TaskID: "exec-1", Type: clientexec.EventError, Error: "stream failed"})
	if model.execTasks[0].State != "error" || !strings.HasSuffix(model.execTasks[0].Output, "stream failed") {
		t.Fatalf("error event task = %#v", model.execTasks[0])
	}
}

func TestUpsertExecTaskPreservesOutput(t *testing.T) {
	t.Parallel()

	items := []execTaskView{{ID: "exec-1", State: taskStateRunning, Output: "existing"}}
	items = upsertExecTask(items, execTaskView{ID: "exec-1", State: "completed"})
	if len(items) != 1 || items[0].State != "completed" || items[0].Output != "existing" {
		t.Fatalf("updated tasks = %#v", items)
	}
	items = upsertExecTask(items, execTaskView{ID: "exec-2", State: taskStateRunning})
	if len(items) != 2 || items[1].ID != "exec-2" {
		t.Fatalf("appended tasks = %#v", items)
	}
}
