package tui

import (
	"errors"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	clientdataplane "github.com/fengqi-dev/kube-loop/internal/client/dataplane"
	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/networkdiag"
)

func (m Model) View() string {
	if m.workspace.initialized {
		return m.viewWorkspace()
	}
	return m.viewConsole()
}

func (m Model) hintText() string {
	if m.mode == viewLogin {
		return HintStyle.Render("↑↓ server  enter select  a add  l login  : cmd  q quit")
	}
	if m.actionMode != actionNone {
		return HintStyle.Render("enter confirm  esc cancel")
	}
	switch m.activeTab {
	case tabConnection:
		return HintStyle.Render(": cmd  enter connect  m mode  n ns  :servers")
	case tabWorkloads:
		return HintStyle.Render(": cmd  / filter  f forward  s ssh")
	case tabServices:
		return HintStyle.Render(": cmd  / filter  f forward  x exchange  m mirror  p preview")
	case tabTasks:
		return HintStyle.Render(": cmd  / filter  p pause  r resume  d delete  y copy")
	case tabCount:
		return HintStyle.Render("r refresh")
	}
	return HintStyle.Render("r refresh")
}

func (m Model) connected() bool {
	state := m.dataPlaneStatus.State
	return state == dataPlaneStateConnected || state == "active" || state == "reconnecting"
}

func (m Model) connectDataPlane() tea.Cmd {
	return func() tea.Msg {
		profile, ok := m.state.ActiveProfile()
		if !ok {
			return dataPlaneConnectedMsg{stage: stageCreateClusterSession, err: fmt.Errorf("no active server")}
		}
		namespace := m.namespace
		if namespace == "" {
			namespace = profile.LastNamespace
		}
		if !containsNamespace(m.namespaces, namespace) && len(m.namespaces) > 0 {
			namespace = m.namespaces[0].Name
		}
		if namespace == "" {
			return dataPlaneConnectedMsg{stage: stageCreateClusterSession, err: fmt.Errorf("no namespace selected")}
		}
		session, err := m.state.sessions.Connect(m.state.ctx, profile, namespace)
		if err != nil {
			return dataPlaneConnectedMsg{stage: stageCreateClusterSession, err: err}
		}
		if m.selectedMode == clientdataplane.ModeTUN {
			for _, issue := range networkdiag.InspectNetworkSpec(session.NetworkSpec).Issues {
				if issue.Severity == networkdiag.SeverityWarning {
					return dataPlaneConnectedMsg{
						stage: "Validate TUN network",
						err:   fmt.Errorf("cannot install TUN: %s", issue.Message),
					}
				}
			}
		}
		return dataPlaneSessionConnectedMsg{profile: profile, session: session, mode: m.selectedMode}
	}
}

func (m Model) connectSOCKSDataPlane(message dataPlaneSessionConnectedMsg) tea.Cmd {
	return func() tea.Msg {
		status, err := m.state.dataPlanes.Connect(m.state.ctx, message.profile, message.session)
		if err != nil {
			return dataPlaneConnectedMsg{stage: "Start SOCKS Data Plane", err: err}
		}
		if message.mode == clientdataplane.ModeSOCKS {
			return dataPlaneConnectedMsg{status: status}
		}
		return dataPlaneSOCKSConnectedMsg{profileID: message.profile.ID}
	}
}

func (m Model) connectTUNDataPlane(profileID string) tea.Cmd {
	return func() tea.Msg {
		status, err := m.state.dataPlanes.SwitchMode(m.state.ctx, profileID, clientdataplane.ModeTUN)
		if err != nil {
			err = errors.Join(err, m.state.dataPlanes.Disconnect(profileID))
		}
		return dataPlaneConnectedMsg{status: status, stage: "Start TUN Helper Service", err: err}
	}
}

func (m *Model) beginConnectionProgress() {
	m.setConnectionProgress(1, connectionProgressSteps(m.selectedMode), "Creating Cluster Session")
}

func (m *Model) setConnectionProgress(step, total int, label string) {
	m.status = fmt.Sprintf("[%d/%d] %s...", step, total, label)
}

func connectionProgressSteps(mode clientdataplane.Mode) int {
	if mode == clientdataplane.ModeTUN {
		return 3
	}
	return 2
}
func (m Model) disconnectDataPlane() tea.Cmd {
	return func() tea.Msg {
		err := m.state.dataPlanes.Disconnect(m.activeProfile.ID)
		return dataPlaneDisconnectedMsg{
			status: clientdataplane.Status{
				State: dataPlaneStateDisconnected,
				Mode:  clientdataplane.ModeSOCKS,
			},
			err: err,
		}
	}
}
func (m Model) switchDataPlaneMode(previous clientdataplane.Mode) tea.Cmd {
	return func() tea.Msg {
		if m.selectedMode == clientdataplane.ModeTUN {
			session, err := m.state.sessions.Current(m.activeProfile.ID)
			if err != nil {
				return dataPlaneModeMsg{previous: previous, err: err}
			}
			for _, issue := range networkdiag.InspectNetworkSpec(session.NetworkSpec).Issues {
				if issue.Severity == networkdiag.SeverityWarning {
					return dataPlaneModeMsg{
						previous: previous,
						err:      fmt.Errorf("cannot install TUN: %s", issue.Message),
					}
				}
			}
		}
		status, err := m.state.dataPlanes.SwitchMode(m.state.ctx, m.activeProfile.ID, m.selectedMode)
		return dataPlaneModeMsg{status: status, previous: previous, err: err}
	}
}
func containsNamespace(items []clientremote.Namespace, name string) bool {
	for _, item := range items {
		if item.Name == name {
			return true
		}
	}
	return false
}
func tickCmd() tea.Cmd {
	return tea.Tick(5*time.Second, func(t time.Time) tea.Msg { return tickMsg{time: t} })
}
