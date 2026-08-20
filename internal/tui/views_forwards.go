package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) updateTasks(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < m.consoleItemCount()-1 {
			m.cursor++
		}
	case "d", "enter":
		row, ok := m.selectedConsoleTask()
		if !ok {
			return m, nil
		}
		m.loading, m.status = true, "Stopping session..."
		return m, tea.Batch(m.spinner.Tick, m.stopTaskAt(row.index))
	}
	return m, nil
}

func (m Model) stopTaskAt(index int) tea.Cmd {
	return func() tea.Msg {
		if index < len(m.portForwards) {
			task := m.portForwards[index]
			return taskStoppedMsg{kind: "forward", id: task.ID, err: m.state.forwards.Stop(m.state.ctx, m.activeProfile.ID, task.ID)}
		}
		index -= len(m.portForwards)
		if index < len(m.exchanges) {
			task := m.exchanges[index]
			return taskStoppedMsg{kind: "exchange", id: task.ID, err: m.state.exchanges.Stop(m.state.ctx, m.activeProfile.ID, task.ID)}
		}
		index -= len(m.exchanges)
		if index < len(m.mirrors) {
			task := m.mirrors[index]
			return taskStoppedMsg{kind: "mirror", id: task.ID, err: m.state.mirrors.Stop(m.state.ctx, m.activeProfile.ID, task.ID)}
		}
		index -= len(m.mirrors)
		if index < len(m.previews) {
			task := m.previews[index]
			return taskStoppedMsg{kind: "preview", id: task.ID, err: m.state.previews.Stop(m.state.ctx, m.activeProfile.ID, task.ID)}
		}
		index -= len(m.previews)
		if index < len(m.podSSHEndpoints) {
			task := m.podSSHEndpoints[index]
			return taskStoppedMsg{kind: "ssh", id: task.ID, err: m.state.podSSH.Stop(m.activeProfile.ID, task.ID)}
		}
		index -= len(m.podSSHEndpoints)
		if index < len(m.execTasks) {
			task := m.execTasks[index]
			if task.State != "running" {
				return taskStoppedMsg{kind: "exec", id: task.ID}
			}
			return taskStoppedMsg{kind: "exec", id: task.ID, err: m.state.execs.Stop(m.activeProfile.ID, task.ID)}
		}
		return taskStoppedMsg{err: fmt.Errorf("session selection is invalid")}
	}
}

func (m Model) selectedExecTask() (execTaskView, bool) {
	row, ok := m.selectedConsoleTask()
	if !ok || row.kind != "exec" {
		return execTaskView{}, false
	}
	index := row.index - len(m.portForwards) - len(m.exchanges) - len(m.mirrors) - len(m.previews) - len(m.podSSHEndpoints)
	if index < 0 || index >= len(m.execTasks) {
		return execTaskView{}, false
	}
	return m.execTasks[index], true
}
