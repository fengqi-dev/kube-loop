package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	clientprofile "github.com/fengqi-dev/kube-loop/internal/client/profile"
)

func (m Model) updateSessionMessage(message tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := message.(type) {
	case profilesLoadedMsg:
		m.profiles = msg.state
		if m.login.cursor >= len(msg.state.Profiles) {
			m.login.cursor = max(0, len(msg.state.Profiles)-1)
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
			m.login.profileSelectionPending = false
			m.authSession, m.err = AuthSession{}, msg.err.Error()
			return m, nil, true
		}
		m.authSession = msg.session
		if m.login.profileSelectionPending {
			m.login.profileSelectionPending = false
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
		m.loading, m.login.cancel = false, nil
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
