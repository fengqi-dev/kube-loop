package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	clientdataplane "github.com/fengqi-dev/kube-loop/internal/client/dataplane"
)

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
