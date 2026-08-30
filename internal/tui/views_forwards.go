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
	case keyDown, "j":
		if m.cursor < m.consoleItemCount()-1 {
			m.cursor++
		}
	case "p", keyEnter:
		row, ok := m.selectedConsoleTask()
		if !ok {
			return m, nil
		}
		m.loading, m.status = true, "Pausing session..."
		return m, tea.Batch(m.spinner.Tick, m.pauseTaskAt(row.index))
	case "r":
		row, ok := m.selectedConsoleTask()
		if !ok {
			return m, nil
		}
		m.loading, m.status = true, "Resuming session..."
		return m, tea.Batch(m.spinner.Tick, m.resumeTaskAt(row.index))
	}
	return m, nil
}

func (m Model) pauseTaskAt(index int) tea.Cmd {
	return func() tea.Msg {
		if index < len(m.portForwards) {
			task := m.portForwards[index]
			return taskLifecycleMsg{action: taskActionPaused,
				kind: taskKindForward,
				id:   task.ID,
				err:  m.state.forwards.Pause(m.state.ctx, m.activeProfile.ID, task.ID),
			}
		}
		index -= len(m.portForwards)
		if index < len(m.exchanges) {
			task := m.exchanges[index]
			return taskLifecycleMsg{action: taskActionPaused,
				kind: taskKindExchange,
				id:   task.ID,
				err:  m.state.exchanges.Pause(m.state.ctx, m.activeProfile.ID, task.ID),
			}
		}
		index -= len(m.exchanges)
		if index < len(m.mirrors) {
			task := m.mirrors[index]
			return taskLifecycleMsg{action: taskActionPaused,
				kind: taskKindMirror,
				id:   task.ID,
				err:  m.state.mirrors.Pause(m.state.ctx, m.activeProfile.ID, task.ID),
			}
		}
		index -= len(m.mirrors)
		if index < len(m.previews) {
			task := m.previews[index]
			return taskLifecycleMsg{action: taskActionPaused,
				kind: taskKindPreview,
				id:   task.ID,
				err:  m.state.previews.Pause(m.state.ctx, m.activeProfile.ID, task.ID),
			}
		}
		index -= len(m.previews)
		if index < len(m.podSSHEndpoints) {
			task := m.podSSHEndpoints[index]
			return taskLifecycleMsg{
				action: taskActionPaused,
				kind:   taskKindSSH,
				id:     task.ID,
				err:    m.state.podSSH.Stop(m.activeProfile.ID, task.ID),
			}
		}
		index -= len(m.podSSHEndpoints)
		if index < len(m.execTasks) {
			task := m.execTasks[index]
			if task.State != taskStateRunning {
				return taskLifecycleMsg{action: taskActionPaused, kind: taskKindExec, id: task.ID}
			}
			return taskLifecycleMsg{
				action: taskActionPaused,
				kind:   taskKindExec,
				id:     task.ID,
				err:    m.state.execs.Stop(m.activeProfile.ID, task.ID),
			}
		}
		index -= len(m.previews)
		if index < len(m.podSSHEndpoints) {
			task := m.podSSHEndpoints[index]
			return taskLifecycleMsg{
				action: taskStateStopped,
				kind:   taskKindSSH,
				id:     task.ID,
				err:    m.state.podSSH.Stop(m.activeProfile.ID, task.ID),
			}
		}
		index -= len(m.podSSHEndpoints)
		if index < len(m.execTasks) {
			task := m.execTasks[index]
			if task.State != taskStateRunning {
				return taskLifecycleMsg{action: taskStateStopped, kind: taskKindExec, id: task.ID}
			}
			return taskLifecycleMsg{
				action: taskStateStopped,
				kind:   taskKindExec,
				id:     task.ID,
				err:    m.state.execs.Stop(m.activeProfile.ID, task.ID),
			}
		}
		return taskLifecycleMsg{
			action: taskActionPaused,
			err:    fmt.Errorf("session selection is invalid"),
		}
	}
}

func (m Model) resumeTaskAt(index int) tea.Cmd {
	return func() tea.Msg {
		if index < len(m.portForwards) {
			task := m.portForwards[index]
			_, err := m.state.forwards.Resume(m.state.ctx, m.activeProfile.ID, task.ID)
			return taskLifecycleMsg{
				action: taskActionResumed,
				kind:   taskKindForward,
				id:     task.ID,
				err:    err,
			}
		}
		index -= len(m.portForwards)
		if index < len(m.exchanges) {
			task := m.exchanges[index]
			_, err := m.state.exchanges.Resume(m.state.ctx, m.activeProfile.ID, task.ID)
			return taskLifecycleMsg{
				action: taskActionResumed,
				kind:   taskKindExchange,
				id:     task.ID,
				err:    err,
			}
		}
		index -= len(m.exchanges)
		if index < len(m.mirrors) {
			task := m.mirrors[index]
			_, err := m.state.mirrors.Resume(m.state.ctx, m.activeProfile.ID, task.ID)
			return taskLifecycleMsg{
				action: taskActionResumed,
				kind:   taskKindMirror,
				id:     task.ID,
				err:    err,
			}
		}
		index -= len(m.mirrors)
		if index < len(m.previews) {
			task := m.previews[index]
			_, err := m.state.previews.Resume(m.state.ctx, m.activeProfile.ID, task.ID)
			return taskLifecycleMsg{
				action: taskActionResumed,
				kind:   taskKindPreview,
				id:     task.ID,
				err:    err,
			}
		}
		return taskLifecycleMsg{
			action: taskActionResumed,
			err:    fmt.Errorf("selected session cannot be resumed"),
		}
	}
}

func (m Model) deleteTaskAt(index int) tea.Cmd {
	return func() tea.Msg {
		if index < len(m.portForwards) {
			task := m.portForwards[index]
			return taskLifecycleMsg{action: taskActionDeleted, kind: taskKindForward, id: task.ID,
				err: m.state.forwards.Delete(m.state.ctx, m.activeProfile.ID, task.ID)}
		}
		index -= len(m.portForwards)
		if index < len(m.exchanges) {
			task := m.exchanges[index]
			return taskLifecycleMsg{action: taskActionDeleted, kind: taskKindExchange, id: task.ID,
				err: m.state.exchanges.Delete(m.state.ctx, m.activeProfile.ID, task.ID)}
		}
		index -= len(m.exchanges)
		if index < len(m.mirrors) {
			task := m.mirrors[index]
			return taskLifecycleMsg{action: taskActionDeleted, kind: taskKindMirror, id: task.ID,
				err: m.state.mirrors.Delete(m.state.ctx, m.activeProfile.ID, task.ID)}
		}
		index -= len(m.mirrors)
		if index < len(m.previews) {
			task := m.previews[index]
			return taskLifecycleMsg{action: taskActionDeleted, kind: taskKindPreview, id: task.ID,
				err: m.state.previews.Delete(m.state.ctx, m.activeProfile.ID, task.ID)}
		}
		return taskLifecycleMsg{
			action: taskActionDeleted,
			err:    fmt.Errorf("selected session cannot be deleted"),
		}
	}
}
