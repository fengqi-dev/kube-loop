package tui

import (
	"encoding/base64"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	clientexec "github.com/fengqi-dev/kube-loop/internal/client/exec"
)

func waitExecEvent(state *State) tea.Cmd {
	return func() tea.Msg {
		select {
		case event := <-state.execEvents:
			return execEventMsg{event: event}
		case <-state.ctx.Done():
			return nil
		}
	}
}

func (m *Model) applyExecEvent(event clientexec.Event) {
	index := -1
	for i := range m.execTasks {
		if m.execTasks[i].ID == event.TaskID {
			index = i
			break
		}
	}
	if index < 0 {
		m.execTasks = append(m.execTasks, execTaskView{ID: event.TaskID, State: taskStateRunning})
		index = len(m.execTasks) - 1
	}
	switch event.Type {
	case clientexec.EventStdout, clientexec.EventStderr:
		data, err := base64.StdEncoding.DecodeString(event.Data)
		if err == nil {
			m.execTasks[index].Output += string(data)
			if len(m.execTasks[index].Output) > 8192 {
				m.execTasks[index].Output = m.execTasks[index].Output[len(m.execTasks[index].Output)-8192:]
			}
		}
	case clientexec.EventExit:
		m.execTasks[index].State = fmt.Sprintf("exit %d", event.ExitCode)
	case clientexec.EventError:
		m.execTasks[index].State, m.execTasks[index].Output = "error", m.execTasks[index].Output+event.Error
	}
}

func upsertExecTask(items []execTaskView, item execTaskView) []execTaskView {
	for i := range items {
		if items[i].ID == item.ID {
			output := items[i].Output
			items[i] = item
			items[i].Output = output
			return items
		}
	}
	return append(items, item)
}
