package tui

import (
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	clientdataplane "github.com/fengqi-dev/kube-loop/internal/client/dataplane"
	clientexchange "github.com/fengqi-dev/kube-loop/internal/client/exchange"
	clientexec "github.com/fengqi-dev/kube-loop/internal/client/exec"
	clientmirror "github.com/fengqi-dev/kube-loop/internal/client/mirror"
	clientpodssh "github.com/fengqi-dev/kube-loop/internal/client/podssh"
	clientportforward "github.com/fengqi-dev/kube-loop/internal/client/portforward"
	clientpreview "github.com/fengqi-dev/kube-loop/internal/client/preview"
	clientprofile "github.com/fengqi-dev/kube-loop/internal/client/profile"
	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/networkdiag"
)

type tab int

const (
	tabConnection tab = iota
	tabWorkloads
	tabServices
	tabTasks
	tabCount
)

var tabNames = []string{"Connection", "Workloads", "Services", "Sessions"}

type viewMode int

const (
	viewLogin viewMode = iota
	viewMain
)

type actionMode int

const (
	actionNone actionMode = iota
	actionPortForward
	actionExec
	actionExchange
	actionMirror
	actionPreview
)

type actionPortOption struct {
	Name     string
	Port     int32
	Protocol string
}

type execTaskView struct {
	ID      string
	Pod     string
	Command string
	State   string
	Output  string
}

type Model struct {
	state *State

	width, height           int
	profiles                clientprofile.State
	activeProfile           clientprofile.Profile
	authSession             AuthSession
	namespace               string
	pendingNamespace        string
	pendingNamespaceSet     bool
	namespaceReturnResource workspaceResource
	namespaces              []clientremote.Namespace
	pods                    []clientremote.Pod
	services                []clientremote.Service
	dataPlaneStatus         clientdataplane.Status
	selectedMode            clientdataplane.Mode
	portForwards            []clientportforward.Info
	exchanges               []clientexchange.Info
	mirrors                 []clientmirror.Info
	previews                []clientpreview.Info
	podSSHEndpoints         []clientpodssh.Info
	execTasks               []execTaskView

	mode        viewMode
	activeTab   tab
	cursor      int
	console     consoleState
	workspace   workspaceState
	err         string
	status      string
	loading     bool
	autoConnect bool
	spinner     spinner.Model

	loginCursor int
	loginURL    string
	loginAdding bool
	loginCancel func()

	actionMode        actionMode
	actionService     string
	actionPod         string
	actionContainer   string
	actionPort        int32
	actionProtocol    string
	actionPorts       []actionPortOption
	actionPortIndex   int
	actionLocalPort   string
	actionLocalHost   string
	actionPreviewName string
	actionServicePort string
	actionField       int
	actionCommand     string
}

func New(state *State) Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	profiles := state.Snapshot()
	activeProfile, _ := state.ActiveProfile()
	workspace := newWorkspaceState(state.configPath)
	workspace.resource = resourceProfiles
	workspace.history = []workspaceResource{resourceProfiles}
	return Model{
		state: state, spinner: sp, profiles: profiles, workspace: workspace,
		activeProfile: activeProfile, selectedMode: clientdataplane.ModeTUN, mode: viewLogin,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, loadAuthStatus(m), tickCmd(), waitExecEvent(m.state))
}

func (m Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if m.workspace.initialized {
		if cmd, handled := m.updateWorkspace(message); handled {
			return m, cmd
		}
	} else if cmd, handled := m.updateConsole(message); handled {
		return m, cmd
	}

	switch msg := message.(type) {
	case clipboardCopiedMsg:
		if msg.err != nil {
			m.status = ""
			if msg.kind == "ERROR" {
				m.err = "copy error: " + msg.err.Error()
			} else {
				m.err = "copy session: " + msg.err.Error()
			}
			return m, nil
		}
		m.err = ""
		if msg.kind == "ERROR" {
			m.status = "Error copied to clipboard"
		} else {
			m.status = fmt.Sprintf("%s session copied to clipboard", msg.kind)
		}
		return m, nil
	case workspaceLoadedMsg:
		if msg.resource != m.workspace.resource || msg.generation != m.workspace.loadGeneration {
			return m, nil
		}
		return m.Update(msg.message)
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" || (msg.String() == "q" && !m.loginAdding && m.actionMode == actionNone) {
			return m, tea.Quit
		}
		if m.mode == viewLogin {
			return m.updateLogin(msg)
		}
		if m.actionMode != actionNone {
			return m.updateAction(msg)
		}
		switch msg.String() {
		case "tab":
			m.activeTab = (m.activeTab + 1) % tabCount
			m.cursor, m.err, m.status, m.loading = 0, "", "", true
			return m, tea.Batch(m.spinner.Tick, m.loadTabData())
		case "shift+tab":
			m.activeTab = (m.activeTab - 1 + tabCount) % tabCount
			m.cursor, m.err, m.status, m.loading = 0, "", "", true
			return m, tea.Batch(m.spinner.Tick, m.loadTabData())
		case "r":
			m.loading = true
			return m, tea.Batch(m.spinner.Tick, m.loadTabData())
		case "esc":
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
		}
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case profilesLoadedMsg:
		m.profiles = msg.state
		if m.loginCursor >= len(msg.state.Profiles) {
			m.loginCursor = max(0, len(msg.state.Profiles)-1)
		}
		for _, profile := range msg.state.Profiles {
			if profile.ID == msg.state.ActiveProfileID {
				m.activeProfile = profile
				return m, loadAuthStatus(m)
			}
		}
		m.activeProfile = clientprofile.Profile{}
		return m, nil
	case authStatusMsg:
		m.loading = false
		if msg.err != nil {
			m.authSession, m.err = AuthSession{}, msg.err.Error()
			return m, nil
		}
		m.authSession = msg.session
		if msg.session.Authenticated && m.mode == viewLogin {
			m.mode, m.loading, m.autoConnect = viewMain, true, true
			return m, m.workspaceNavigate(resourceConnection, true)
		}
		return m, nil
	case loginResultMsg:
		m.loading, m.loginCancel = false, nil
		if msg.cancelled {
			m.err, m.status = "", "Login cancelled"
			return m, nil
		}
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.authSession, m.mode, m.err, m.loading, m.autoConnect = msg.session, viewMain, "", true, true
		return m, m.workspaceNavigate(resourceConnection, true)
	case logoutResultMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.authSession, m.mode, m.err = AuthSession{}, viewLogin, ""
		m.workspace.resource = resourceProfiles
		m.workspace.history, m.workspace.historyPos = []workspaceResource{resourceProfiles}, 0
		return m, loadProfiles(m.state)
	case namespacesLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.autoConnect = false
			m.err = msg.err.Error()
			return m, nil
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
				return m, tea.Batch(m.spinner.Tick, m.connectDataPlane())
			}
		}
		return m, nil
	case namespaceChangedMsg:
		m.namespace, m.cursor, m.err, m.status, m.loading = msg.namespace, 0, "", "", true
		resource := msg.resource
		if resource == "" {
			resource = resourcePods
		}
		m.namespaceReturnResource = ""
		return m, m.workspaceNavigate(resource, true)
	case podsLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err.Error()
		} else {
			m.pods = msg.pods
		}
		return m, nil
	case servicesLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err.Error()
		} else {
			m.services = msg.services
		}
		return m, nil
	case dataPlaneStatusMsg:
		m.loading = false
		m.dataPlaneStatus = msg.status
		if msg.status.Mode != "" && msg.status.State != "disconnected" {
			m.selectedMode = clientdataplane.Mode(msg.status.Mode)
		}
		return m, nil
	case dataPlaneSessionConnectedMsg:
		total := connectionProgressSteps(msg.mode)
		m.setConnectionProgress(2, total, "Starting SOCKS Data Plane")
		return m, tea.Batch(m.spinner.Tick, m.connectSOCKSDataPlane(msg))
	case dataPlaneSOCKSConnectedMsg:
		m.setConnectionProgress(3, 3, "Installing and starting Helper Service")
		return m, tea.Batch(m.spinner.Tick, m.connectTUNDataPlane(msg.profileID))
	case dataPlaneConnectedMsg:
		m.loading = false
		if msg.err != nil {
			m.namespaceReturnResource = ""
			m.err = msg.err.Error()
			if msg.stage != "" {
				m.err = msg.stage + ": " + m.err
			}
			return m, nil
		}
		m.dataPlaneStatus, m.selectedMode = msg.status, clientdataplane.Mode(msg.status.Mode)
		resource := m.namespaceReturnResource
		if resource == "" {
			resource = resourcePods
		}
		m.namespaceReturnResource = ""
		cmd := m.workspaceNavigate(resource, true)
		m.status = "Data plane connected"
		return m, cmd
	case dataPlaneDisconnectedMsg:
		m.loading = false
		if msg.err != nil {
			m.pendingNamespace, m.pendingNamespaceSet, m.namespaceReturnResource = "", false, ""
			m.err = msg.err.Error()
			return m, nil
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
			return m, tea.Batch(m.spinner.Tick, m.connectDataPlane())
		}
		m.status = "Data plane disconnected"
		return m, nil
	case dataPlaneModeMsg:
		m.loading = false
		if msg.err != nil {
			m.selectedMode, m.err = msg.previous, msg.err.Error()
			return m, nil
		}
		m.dataPlaneStatus = msg.status
		m.status = fmt.Sprintf("Mode switched to %s", msg.status.Mode)
		return m, nil
	case portForwardsLoadedMsg:
		m.portForwards = msg.forwards
		return m, nil
	case podSSHLoadedMsg:
		m.podSSHEndpoints = msg.endpoints
		return m, nil
	case trafficOperationsLoadedMsg:
		m.exchanges, m.mirrors, m.previews = msg.exchanges, msg.mirrors, msg.previews
		return m, nil
	case profileSavedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.loginAdding, m.loginURL, m.status = false, "", "Server saved"
		if m.console.overlay == overlayProfileAdd {
			m.console.overlay = overlayProfiles
		}
		return m, loadProfiles(m.state)
	case profileDeletedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.status = "Server deleted"
		return m, loadProfiles(m.state)
	case portForwardStartedMsg:
		m.loading, m.actionMode = false, actionNone
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.status = fmt.Sprintf("Port forward started on %s", msg.info.Address)
		return m, m.workspaceNavigate(resourceTasks, true)
	case trafficOperationStartedMsg:
		m.loading, m.actionMode = false, actionNone
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.status = fmt.Sprintf("%s started for %s", msg.kind, msg.target)
		return m, m.workspaceNavigate(resourceTasks, true)
	case podSSHStartedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.status = "Opening Pod SSH: " + msg.info.Pod
		return m, openPodSSH(msg.info.Command)
	case podSSHExitedMsg:
		if msg.err != nil {
			m.err = "Pod SSH: " + msg.err.Error()
		} else {
			m.status = "Pod SSH closed"
		}
		return m, loadPodSSH(m.state, m.activeProfile.ID)
	case helperServiceUninstalledMsg:
		m.loading = false
		if msg.err != nil {
			m.err = "Uninstall Helper Service: " + msg.err.Error()
			return m, nil
		}
		m.err, m.status = "", "Helper Service uninstalled"
		return m, nil
	case execStartedMsg:
		m.loading, m.actionMode = false, actionNone
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.execTasks = upsertExecTask(m.execTasks, execTaskView{ID: msg.task.ID, Pod: msg.task.Pod, Command: msg.command, State: "running"})
		cmd := m.workspaceNavigate(resourceTasks, true)
		m.status = "Pod exec started"
		return m, cmd
	case execEventMsg:
		m.applyExecEvent(msg.event)
		return m, waitExecEvent(m.state)
	case taskStoppedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		if msg.kind == "exec" {
			for i := range m.execTasks {
				if m.execTasks[i].ID == msg.id {
					m.execTasks[i].State = "stopped"
				}
			}
		}
		m.status = "Session stopped"
		return m, tea.Batch(loadPortForwards(m.state, m.activeProfile.ID), loadPodSSH(m.state, m.activeProfile.ID))
	case tickMsg:
		cmds := []tea.Cmd{tickCmd()}
		if m.mode == viewMain && !m.loading && m.activeProfile.ID != "" && m.authSession.Authenticated {
			cmds = append(cmds, m.beginWorkspaceLoad())
		}
		return m, tea.Batch(cmds...)
	}
	return m, nil
}

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
	default:
		return HintStyle.Render("r refresh")
	}
}

func (m Model) connected() bool {
	state := m.dataPlaneStatus.State
	return state == "connected" || state == "active" || state == "reconnecting"
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
		return tea.Batch(loadPortForwards(m.state, m.activeProfile.ID), loadTrafficOperations(m.state, m.activeProfile.ID), loadPodSSH(m.state, m.activeProfile.ID))
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
			status = clientdataplane.Status{State: "disconnected", Mode: "socks"}
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
		if m.namespace == "" {
			namespaces, err := m.state.remote.Namespaces(m.state.ctx, m.activeProfile)
			if err != nil {
				return podsLoadedMsg{err: err}
			}
			items := make([]clientremote.Pod, 0)
			var firstErr error
			for _, namespace := range namespaces {
				pods, listErr := m.state.remote.Pods(m.state.ctx, m.activeProfile, namespace.Name)
				if listErr != nil {
					if firstErr == nil {
						firstErr = listErr
					}
					continue
				}
				items = append(items, pods...)
			}
			if len(items) == 0 && firstErr != nil {
				return podsLoadedMsg{err: firstErr}
			}
			return podsLoadedMsg{pods: items}
		}
		items, err := m.state.remote.Pods(m.state.ctx, m.activeProfile, m.namespace)
		return podsLoadedMsg{pods: items, err: err}
	}
}
func loadServices(m Model) tea.Cmd {
	return func() tea.Msg {
		if m.namespace == "" {
			namespaces, err := m.state.remote.Namespaces(m.state.ctx, m.activeProfile)
			if err != nil {
				return servicesLoadedMsg{err: err}
			}
			items := make([]clientremote.Service, 0)
			var firstErr error
			for _, namespace := range namespaces {
				services, listErr := m.state.remote.Services(m.state.ctx, m.activeProfile, namespace.Name)
				if listErr != nil {
					if firstErr == nil {
						firstErr = listErr
					}
					continue
				}
				items = append(items, services...)
			}
			if len(items) == 0 && firstErr != nil {
				return servicesLoadedMsg{err: firstErr}
			}
			return servicesLoadedMsg{services: items}
		}
		items, err := m.state.remote.Services(m.state.ctx, m.activeProfile, m.namespace)
		return servicesLoadedMsg{services: items, err: err}
	}
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
			return dataPlaneConnectedMsg{stage: "Create Cluster Session", err: fmt.Errorf("no active server")}
		}
		namespace := m.namespace
		if namespace == "" {
			namespace = profile.LastNamespace
		}
		if !containsNamespace(m.namespaces, namespace) && len(m.namespaces) > 0 {
			namespace = m.namespaces[0].Name
		}
		if namespace == "" {
			return dataPlaneConnectedMsg{stage: "Create Cluster Session", err: fmt.Errorf("no namespace selected")}
		}
		session, err := m.state.sessions.Connect(m.state.ctx, profile, namespace)
		if err != nil {
			return dataPlaneConnectedMsg{stage: "Create Cluster Session", err: err}
		}
		if m.selectedMode == clientdataplane.ModeTUN {
			for _, issue := range networkdiag.InspectNetworkSpec(session.NetworkSpec).Issues {
				if issue.Severity == networkdiag.SeverityWarning {
					return dataPlaneConnectedMsg{stage: "Validate TUN network", err: fmt.Errorf("cannot install TUN: %s", issue.Message)}
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
		return dataPlaneDisconnectedMsg{status: clientdataplane.Status{State: "disconnected", Mode: "socks"}, err: err}
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
					return dataPlaneModeMsg{previous: previous, err: fmt.Errorf("cannot install TUN: %s", issue.Message)}
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
		m.execTasks = append(m.execTasks, execTaskView{ID: event.TaskID, State: "running"})
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

type profilesLoadedMsg struct{ state clientprofile.State }
type authStatusMsg struct {
	session AuthSession
	err     error
}
type loginResultMsg struct {
	session   AuthSession
	err       error
	cancelled bool
}
type logoutResultMsg struct{ err error }
type namespacesLoadedMsg struct {
	namespaces []clientremote.Namespace
	err        error
}
type namespaceChangedMsg struct {
	namespace string
	resource  workspaceResource
}
type podsLoadedMsg struct {
	pods []clientremote.Pod
	err  error
}
type servicesLoadedMsg struct {
	services []clientremote.Service
	err      error
}
type dataPlaneStatusMsg struct{ status clientdataplane.Status }
type dataPlaneSessionConnectedMsg struct {
	profile clientprofile.Profile
	session clientremote.Session
	mode    clientdataplane.Mode
}
type dataPlaneSOCKSConnectedMsg struct{ profileID string }
type dataPlaneConnectedMsg struct {
	status clientdataplane.Status
	stage  string
	err    error
}
type dataPlaneDisconnectedMsg struct {
	status clientdataplane.Status
	err    error
}
type dataPlaneModeMsg struct {
	status   clientdataplane.Status
	previous clientdataplane.Mode
	err      error
}
type portForwardsLoadedMsg struct{ forwards []clientportforward.Info }
type podSSHLoadedMsg struct{ endpoints []clientpodssh.Info }
type trafficOperationsLoadedMsg struct {
	exchanges []clientexchange.Info
	mirrors   []clientmirror.Info
	previews  []clientpreview.Info
}
type profileSavedMsg struct {
	profile clientprofile.Profile
	err     error
}
type profileDeletedMsg struct {
	state clientprofile.State
	err   error
}
type portForwardStartedMsg struct {
	info clientportforward.Info
	err  error
}
type trafficOperationStartedMsg struct {
	kind, target string
	err          error
}
type podSSHStartedMsg struct {
	info clientpodssh.Info
	err  error
}
type execStartedMsg struct {
	task    clientremote.ExecTask
	command string
	err     error
}
type execEventMsg struct{ event clientexec.Event }
type taskStoppedMsg struct {
	kind, id string
	err      error
}
type tickMsg struct{ time time.Time }
