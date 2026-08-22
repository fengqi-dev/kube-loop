package tui

import (
	"errors"
	"fmt"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	clientauth "github.com/fengqi-dev/kube-loop/internal/client/auth"
	clientdataplane "github.com/fengqi-dev/kube-loop/internal/client/dataplane"
	clientprofile "github.com/fengqi-dev/kube-loop/internal/client/profile"
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
	case taskStoppedMsg:
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

func (m Model) updateSessionMessage(message tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := message.(type) {
	case profilesLoadedMsg:
		m.profiles = msg.state
		if m.loginCursor >= len(msg.state.Profiles) {
			m.loginCursor = max(0, len(msg.state.Profiles)-1)
		}
		for _, profile := range msg.state.Profiles {
			if profile.ID == msg.state.ActiveProfileID {
				m.activeProfile = profile
				return m, loadAuthStatus(m), true
			}
		}
		m.activeProfile = clientprofile.Profile{}
		return m, nil, true
	case authStatusMsg:
		m.loading = false
		if msg.err != nil {
			m.profileSelectionPending = false
			m.authSession, m.err = AuthSession{}, msg.err.Error()
			return m, nil, true
		}
		m.authSession = msg.session
		if m.profileSelectionPending {
			m.profileSelectionPending = false
			if msg.session.Authenticated {
				m.mode = viewMain
				return m, m.workspaceNavigate(resourceConnection, true), true
			}
			m.status = "Server selected — press 'l' to login"
			return m, nil, true
		}
		if msg.session.Authenticated && m.mode == viewLogin {
			m.mode, m.loading, m.autoConnect = viewMain, true, true
			return m, m.workspaceNavigate(resourceConnection, true), true
		}
		return m, nil, true
	case loginResultMsg:
		m.loading, m.loginCancel = false, nil
		if msg.cancelled {
			m.err, m.status = "", "Login cancelled"
			return m, nil, true
		}
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil, true
		}
		m.authSession, m.mode, m.err, m.loading, m.autoConnect = msg.session, viewMain, "", true, true
		return m, m.workspaceNavigate(resourceConnection, true), true
	case logoutResultMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil, true
		}
		m.authSession, m.mode, m.err = AuthSession{}, viewLogin, ""
		m.workspace.resource = resourceProfiles
		m.workspace.history, m.workspace.historyPos = []workspaceResource{resourceProfiles}, 0
		return m, loadProfiles(m.state), true
	case namespacesLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.autoConnect = false
			m.err = msg.err.Error()
			return m, nil, true
		}
		m.namespaces = msg.namespaces
		if m.namespace != "" && !containsNamespace(msg.namespaces, m.namespace) {
			m.namespace = ""
		}
		if m.autoConnect {
			m.autoConnect = false
			if !m.connected() {
				m.loading, m.err = true, ""
				m.beginConnectionProgress()
				return m, tea.Batch(m.spinner.Tick, m.connectDataPlane()), true
			}
		}
		return m, nil, true
	case namespaceChangedMsg:
		m.namespace, m.cursor, m.err, m.status, m.loading = msg.namespace, 0, "", "", true
		resource := msg.resource
		if resource == "" {
			resource = resourcePods
		}
		m.namespaceReturnResource = ""
		return m, m.workspaceNavigate(resource, true), true
	case podsLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err.Error()
		} else {
			m.pods = msg.pods
		}
		return m, nil, true
	case servicesLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err.Error()
		} else {
			m.services = msg.services
		}
		return m, nil, true
	default:
		return m, nil, false
	}
}

func (m Model) updateDataPlaneMessage(message tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := message.(type) {
	case dataPlaneStatusMsg:
		m.loading = false
		m.dataPlaneStatus = msg.status
		if msg.status.Mode != "" && msg.status.State != dataPlaneStateDisconnected {
			m.selectedMode = clientdataplane.Mode(msg.status.Mode)
		}
		return m, nil, true
	case dataPlaneSessionConnectedMsg:
		total := connectionProgressSteps(msg.mode)
		m.setConnectionProgress(2, total, "Starting SOCKS Data Plane")
		return m, tea.Batch(m.spinner.Tick, m.connectSOCKSDataPlane(msg)), true
	case dataPlaneSOCKSConnectedMsg:
		m.setConnectionProgress(3, 3, "Installing and starting Helper Service")
		return m, tea.Batch(m.spinner.Tick, m.connectTUNDataPlane(msg.profileID)), true
	case dataPlaneConnectedMsg:
		m.loading = false
		if msg.err != nil {
			m.namespaceReturnResource = ""
			m.err = msg.err.Error()
			if msg.stage != "" {
				m.err = msg.stage + ": " + m.err
			}
			return m, nil, true
		}
		m.dataPlaneStatus, m.selectedMode = msg.status, clientdataplane.Mode(msg.status.Mode)
		resource := m.namespaceReturnResource
		if resource == "" {
			resource = resourcePods
		}
		m.namespaceReturnResource = ""
		cmd := m.workspaceNavigate(resource, true)
		m.status = statusDataPlaneConnected
		return m, cmd, true
	case dataPlaneDisconnectedMsg:
		m.loading = false
		if msg.err != nil {
			m.pendingNamespace, m.pendingNamespaceSet, m.namespaceReturnResource = "", false, ""
			m.err = msg.err.Error()
			return m, nil, true
		}
		m.dataPlaneStatus = msg.status
		if m.pendingNamespaceSet {
			target := m.pendingNamespace
			m.pendingNamespace, m.pendingNamespaceSet = "", false
			m.namespace, m.cursor = target, 0
			if target != "" {
				profile := m.activeProfile
				profile.LastNamespace = target
				_ = m.state.profiles.Upsert(profile)
			}
			label := target
			if label == "" {
				label = "all namespaces"
			}
			m.loading, m.status = true, "Switching to "+label+"..."
			return m, tea.Batch(m.spinner.Tick, m.connectDataPlane()), true
		}
		m.status = "Data plane disconnected"
		return m, nil, true
	case dataPlaneModeMsg:
		m.loading = false
		if msg.err != nil {
			m.selectedMode, m.err = msg.previous, msg.err.Error()
			return m, nil, true
		}
		m.dataPlaneStatus = msg.status
		m.status = fmt.Sprintf("Mode switched to %s", msg.status.Mode)
		return m, nil, true
	default:
		return m, nil, false
	}
}

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
		m.loginAdding, m.loginURL, m.status = false, "", "Server saved"
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
		m.loading, m.actionMode = false, actionNone
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil, true
		}
		m.status = fmt.Sprintf("Port forward started on %s", msg.info.Address)
		return m, m.workspaceNavigate(resourceTasks, true), true
	case trafficOperationStartedMsg:
		m.loading, m.actionMode = false, actionNone
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
		m.loading, m.actionMode = false, actionNone
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
	case taskStoppedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil, true
		}
		if msg.kind == taskKindExec {
			for i := range m.execTasks {
				if m.execTasks[i].ID == msg.id {
					m.execTasks[i].State = "stopped"
				}
			}
		}
		m.status = "Session stopped"
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
