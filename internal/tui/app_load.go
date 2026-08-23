package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	clientdataplane "github.com/fengqi-dev/kube-loop/internal/client/dataplane"
	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
)

func (m Model) loadTabData() tea.Cmd {
	switch m.activeTab {
	case tabConnection:
		return tea.Batch(loadAuthStatus(m), loadDataPlaneStatus(m), loadNamespaces(m))
	case tabWorkloads:
		return tea.Batch(loadPods(m), loadNamespaces(m))
	case tabServices:
		return tea.Batch(loadServices(m), loadNamespaces(m))
	case tabTasks:
		return tea.Batch(
			loadPortForwards(m.state, m.activeProfile.ID),
			loadTrafficOperations(m.state, m.activeProfile.ID),
			loadPodSSH(m.state, m.activeProfile.ID),
		)
	case tabCount:
		return nil
	}
	return nil
}

func loadProfiles(state *State) tea.Cmd {
	return func() tea.Msg { return profilesLoadedMsg{state: state.Snapshot()} }
}

func loadAuthStatus(m Model) tea.Cmd {
	return func() tea.Msg {
		if m.activeProfile.ID == "" {
			return authStatusMsg{}
		}
		session, err := m.state.AuthStatus(m.activeProfile.ID)
		return authStatusMsg{session: session, err: err}
	}
}

func loadDataPlaneStatus(m Model) tea.Cmd {
	return func() tea.Msg {
		if m.activeProfile.ID == "" {
			return dataPlaneStatusMsg{}
		}
		status, err := m.state.dataPlanes.Status(m.activeProfile.ID)
		if err != nil {
			status = clientdataplane.Status{
				State: dataPlaneStateDisconnected,
				Mode:  clientdataplane.ModeSOCKS,
			}
		}
		return dataPlaneStatusMsg{status: status}
	}
}

func loadNamespaces(m Model) tea.Cmd {
	return func() tea.Msg {
		if m.activeProfile.ID == "" || !m.authSession.Authenticated {
			return namespacesLoadedMsg{}
		}
		items, err := m.state.remote.Namespaces(m.state.ctx, m.activeProfile)
		return namespacesLoadedMsg{namespaces: items, err: err}
	}
}

func loadPods(m Model) tea.Cmd {
	return func() tea.Msg {
		if m.namespace != "" {
			items, err := m.state.remote.Pods(m.state.ctx, m.activeProfile, m.namespace)
			return podsLoadedMsg{pods: items, err: err}
		}
		items, err := loadAcrossNamespaces(m, func(namespace string) ([]clientremote.Pod, error) {
			return m.state.remote.Pods(m.state.ctx, m.activeProfile, namespace)
		})
		return podsLoadedMsg{pods: items, err: err}
	}
}

func loadServices(m Model) tea.Cmd {
	return func() tea.Msg {
		if m.namespace != "" {
			items, err := m.state.remote.Services(m.state.ctx, m.activeProfile, m.namespace)
			return servicesLoadedMsg{services: items, err: err}
		}
		items, err := loadAcrossNamespaces(m, func(namespace string) ([]clientremote.Service, error) {
			return m.state.remote.Services(m.state.ctx, m.activeProfile, namespace)
		})
		return servicesLoadedMsg{services: items, err: err}
	}
}

func loadAcrossNamespaces[T any](m Model, load func(string) ([]T, error)) ([]T, error) {
	namespaces, err := m.state.remote.Namespaces(m.state.ctx, m.activeProfile)
	if err != nil {
		return nil, err
	}
	items := make([]T, 0)
	var firstErr error
	for _, namespace := range namespaces {
		loaded, loadErr := load(namespace.Name)
		if loadErr != nil {
			if firstErr == nil {
				firstErr = loadErr
			}
			continue
		}
		items = append(items, loaded...)
	}
	if len(items) == 0 {
		return items, firstErr
	}
	return items, nil
}

func loadPortForwards(state *State, profileID string) tea.Cmd {
	return func() tea.Msg { return portForwardsLoadedMsg{forwards: state.forwards.List(profileID)} }
}

func loadPodSSH(state *State, profileID string) tea.Cmd {
	return func() tea.Msg { return podSSHLoadedMsg{endpoints: state.podSSH.List(profileID)} }
}
