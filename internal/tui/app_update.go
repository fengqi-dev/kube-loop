package tui

import (
	"errors"
	"fmt"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	clientauth "github.com/fengqi-dev/kube-loop/internal/client/auth"
	clientdataplane "github.com/fengqi-dev/kube-loop/internal/client/dataplane"
)

func (m Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if errors.Is(tuiMessageError(message), clientauth.ErrLoginExpired) {
		return m.returnToLoginAfterExpiry()
	}
	if m.workspace.initialized {
		if cmd, handled := m.updateWorkspace(message); handled {
			return m, cmd
		}
	} else if cmd, handled := m.updateConsole(message); handled {
		return m, cmd
	}
	if next, cmd, handled := m.updateInterfaceMessage(message); handled {
		return next, cmd
	}
	if next, cmd, handled := m.updateSessionMessage(message); handled {
		return next, cmd
	}
	if next, cmd, handled := m.updateDataPlaneMessage(message); handled {
		return next, cmd
	}
	if next, cmd, handled := m.updateTaskMessage(message); handled {
		return next, cmd
	}
	return m, nil
}

func (m Model) returnToLoginAfterExpiry() (tea.Model, tea.Cmd) {
	m.loading, m.autoConnect = false, false
	m.authSession, m.mode = AuthSession{}, viewLogin
	m.err, m.status = "", clientauth.ErrLoginExpired.Error()
	m.dataPlaneStatus = clientdataplane.Status{
		State: dataPlaneStateDisconnected,
		Mode:  clientdataplane.ModeSOCKS,
	}
	m.workspace.resource = resourceProfiles
	m.workspace.history, m.workspace.historyPos = []workspaceResource{resourceProfiles}, 0
	return m, loadProfiles(m.state)
}

func tuiMessageError(message tea.Msg) error {
	switch msg := message.(type) {
	case workspaceLoadedMsg:
		return tuiMessageError(msg.message)
	case authStatusMsg:
		return msg.err
	case loginResultMsg:
		return msg.err
	case logoutResultMsg:
		return msg.err
	case namespacesLoadedMsg:
		return msg.err
	case podsLoadedMsg:
		return msg.err
	case servicesLoadedMsg:
		return msg.err
	case dataPlaneConnectedMsg:
		return msg.err
	case dataPlaneDisconnectedMsg:
		return msg.err
	case dataPlaneModeMsg:
		return msg.err
	case profileSavedMsg:
		return msg.err
	case profileDeletedMsg:
		return msg.err
	case portForwardStartedMsg:
		return msg.err
	case trafficOperationStartedMsg:
		return msg.err
	case podSSHStartedMsg:
		return msg.err
	case podSSHExitedMsg:
		return msg.err
	case helperServiceUninstalledMsg:
		return msg.err
	case execStartedMsg:
		return msg.err
	case taskLifecycleMsg:
		return msg.err
	default:
		return nil
	}
}

func (m Model) updateInterfaceMessage(message tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := message.(type) {
	case clipboardCopiedMsg:
		return m.updateClipboardCopied(msg), nil, true
	case workspaceLoadedMsg:
		if msg.resource != m.workspace.resource || msg.generation != m.workspace.loadGeneration {
			return m, nil, true
		}
		next, cmd := m.Update(msg.message)
		return next, cmd, true
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil, true
	case tea.KeyMsg:
		next, cmd := m.updateKeyMessage(msg)
		return next, cmd, true
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd, true
	default:
		return m, nil, false
	}
}

func (m Model) updateClipboardCopied(msg clipboardCopiedMsg) Model {
	if msg.err != nil {
		m.status = ""
		if msg.kind == "ERROR" {
			m.err = "copy error: " + msg.err.Error()
		} else {
			m.err = "copy session: " + msg.err.Error()
		}
		return m
	}
	m.err = ""
	if msg.kind == "ERROR" {
		m.status = "Error copied to clipboard"
	} else {
		m.status = fmt.Sprintf("%s session copied to clipboard", msg.kind)
	}
	return m
}

func (m Model) updateKeyMessage(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == keyCtrlC || (msg.String() == "q" && !m.loginAdding && m.actionMode == actionNone) {
		return m, tea.Quit
	}
	if m.mode == viewLogin {
		return m.updateLogin(msg)
	}
	if m.actionMode != actionNone {
		return m.updateAction(msg)
	}
	switch msg.String() {
	case keyTab:
		m.activeTab = (m.activeTab + 1) % tabCount
		m.cursor, m.err, m.status, m.loading = 0, "", "", true
		return m, tea.Batch(m.spinner.Tick, m.loadTabData())
	case keyShiftTab:
		m.activeTab = (m.activeTab - 1 + tabCount) % tabCount
		m.cursor, m.err, m.status, m.loading = 0, "", "", true
		return m, tea.Batch(m.spinner.Tick, m.loadTabData())
	case "r":
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, m.loadTabData())
	case keyEsc:
		m.err, m.status = "", ""
		return m, nil
	}
	switch m.activeTab {
	case tabConnection:
		return m.updateOverview(msg)
	case tabWorkloads:
		return m.updatePods(msg)
	case tabServices:
		return m.updateServices(msg)
	case tabTasks:
		return m.updateTasks(msg)
	case tabCount:
		return m, nil
	}
	return m, nil
}
