package tui

import (
	"context"
	"fmt"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	clientdataplane "github.com/fengqi-dev/kube-loop/internal/client/dataplane"
	clientexchange "github.com/fengqi-dev/kube-loop/internal/client/exchange"
	clientmirror "github.com/fengqi-dev/kube-loop/internal/client/mirror"
	clientpodssh "github.com/fengqi-dev/kube-loop/internal/client/podssh"
	clientportforward "github.com/fengqi-dev/kube-loop/internal/client/portforward"
	clientpreview "github.com/fengqi-dev/kube-loop/internal/client/preview"
	clientprofile "github.com/fengqi-dev/kube-loop/internal/client/profile"
	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
)

type tab int

const (
	tabConnection tab = iota
	tabWorkloads
	tabServices
	tabTasks
	tabCount
)

var tabNames = []string{tabNameConnection, "Workloads", "Services", "Sessions"}

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

//nolint:recvcheck // Bubble Tea requires value-receiver Init, Update, and View; internal mutators use pointers.
type Model struct {
	state   *State
	version string

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

	loginCursor             int
	loginURL                string
	loginAdding             bool
	loginCancel             func()
	profileSelectionPending bool

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

func requireModel(next tea.Model) Model {
	model, ok := next.(Model)
	if !ok {
		panic(fmt.Sprintf("unexpected Bubble Tea model type %T", next))
	}
	return model
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
		state: state, version: state.version, spinner: sp, profiles: profiles, workspace: workspace,
		activeProfile: activeProfile, selectedMode: clientdataplane.ModeTUN, mode: viewLogin,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, loadAuthStatus(m), tickCmd(), waitExecEvent(m.state))
}

func (m Model) context() context.Context {
	if m.state != nil && m.state.ctx != nil {
		return m.state.ctx
	}
	return context.Background()
}
