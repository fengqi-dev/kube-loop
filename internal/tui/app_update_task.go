package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) updateTaskMessage(message tea.Msg) (tea.Model, tea.Cmd, bool) {
	if next, cmd, handled := m.updateTaskListMessage(message); handled {
		return next, cmd, true
	}
	return m.updateTaskOperationMessage(message)
}

func (m Model) updateTaskListMessage(message tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := message.(type) {
	case portForwardsLoadedMsg:
		m.portForwards = msg.forwards
		return m, nil, true
	case podSSHLoadedMsg:
		m.podSSHEndpoints = msg.endpoints
		return m, nil, true
	case trafficOperationsLoadedMsg:
		m.exchanges, m.mirrors, m.previews = msg.exchanges, msg.mirrors, msg.previews
		return m, nil, true
	case profileSavedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil, true
		}
		m.login.adding, m.login.url, m.status = false, "", "Server saved"
		if m.console.overlay == overlayProfileAdd {
			m.console.overlay = overlayProfiles
		}
		return m, loadProfiles(m.state), true
	case profileDeletedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil, true
		}
		m.status = "Server deleted"
		return m, loadProfiles(m.state), true
	default:
		return m, nil, false
	}
}

func (m Model) updateTaskOperationMessage(message tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := message.(type) {
	case portForwardStartedMsg:
		m.loading, m.action.mode = false, actionNone
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil, true
		}
		m.status = fmt.Sprintf("Port forward started on %s", msg.info.Address)
		return m, m.workspaceNavigate(resourceTasks, true), true
	case trafficOperationStartedMsg:
		m.loading, m.action.mode = false, actionNone
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil, true
		}
		m.status = fmt.Sprintf("%s started for %s", msg.kind, msg.target)
		return m, m.workspaceNavigate(resourceTasks, true), true
	case podSSHStartedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil, true
		}
		m.status = "Opening Pod SSH: " + msg.info.Pod
		return m, openPodSSH(m.state.ctx, msg.info.Command), true
	case podSSHExitedMsg:
		if msg.err != nil {
			m.err = "Pod SSH: " + msg.err.Error()
		} else {
			m.status = "Pod SSH closed"
		}
		return m, loadPodSSH(m.state, m.activeProfile.ID), true
	case helperServiceUninstalledMsg:
		m.loading = false
		if msg.err != nil {
			m.err = "Uninstall Helper Service: " + msg.err.Error()
			return m, nil, true
		}
		m.err, m.status = "", "Helper Service uninstalled"
		return m, nil, true
	case execStartedMsg:
		m.loading, m.action.mode = false, actionNone
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil, true
		}
		m.execTasks = upsertExecTask(m.execTasks, execTaskView{
			ID: msg.task.ID, Pod: msg.task.Pod, Command: msg.command, State: taskStateRunning,
		})
		cmd := m.workspaceNavigate(resourceTasks, true)
		m.status = "Pod exec started"
		return m, cmd, true
	case execEventMsg:
		m.applyExecEvent(msg.event)
		return m, waitExecEvent(m.state), true
	case taskLifecycleMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil, true
		}
		if msg.kind == taskKindExec {
			for i := range m.execTasks {
				if m.execTasks[i].ID == msg.id {
					m.execTasks[i].State = taskStateStopped
				}
			}
		}
		m.status = "Session " + msg.action
		return m, tea.Batch(
			loadPortForwards(m.state, m.activeProfile.ID),
			loadPodSSH(m.state, m.activeProfile.ID),
		), true
	case tickMsg:
		cmds := []tea.Cmd{tickCmd()}
		if m.mode == viewMain && !m.loading && m.activeProfile.ID != "" && m.authSession.Authenticated {
			cmds = append(cmds, m.beginWorkspaceLoad())
		}
		return m, tea.Batch(cmds...), true
	default:
		return m, nil, false
	}
}
