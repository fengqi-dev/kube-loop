package tui

import (
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	clientdataplane "github.com/fengqi-dev/kube-loop/internal/client/dataplane"
	clientexec "github.com/fengqi-dev/kube-loop/internal/client/exec"
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
		return HintStyle.Render(": cmd  / filter  enter/d stop  y copy")
	case tabCount:
		return HintStyle.Render("r refresh")
	}
	return HintStyle.Render("r refresh")
}

func (m Model) connected() bool {
	state := m.dataPlaneStatus.State
	return state == dataPlaneStateConnected || state == "active" || state == "reconnecting"
}

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
