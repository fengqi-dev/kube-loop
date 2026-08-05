package mcp

import (
	"context"

	"github.com/fengqi-dev/kube-loop/internal/cluster"
	"github.com/fengqi-dev/kube-loop/internal/filemanager"
	"github.com/fengqi-dev/kube-loop/internal/helper"
	"github.com/fengqi-dev/kube-loop/internal/intercept"
	"github.com/fengqi-dev/kube-loop/internal/portfwd"
	"github.com/fengqi-dev/kube-loop/internal/session"
	"github.com/fengqi-dev/kube-loop/internal/store"
)

type PodCommandRequest struct {
	Context        string
	Namespace      string
	Pod            string
	PodUID         string
	Container      string
	Command        string
	TimeoutSeconds int
}

type PodCommandResult struct {
	Context         string `json:"context"`
	Namespace       string `json:"namespace"`
	Pod             string `json:"pod"`
	Container       string `json:"container"`
	Command         string `json:"command"`
	Stdout          string `json:"stdout"`
	Stderr          string `json:"stderr"`
	ExitCode        int    `json:"exitCode"`
	Error           string `json:"error,omitempty"`
	StdoutTruncated bool   `json:"stdoutTruncated,omitempty"`
	StderrTruncated bool   `json:"stderrTruncated,omitempty"`
}

// Backend is the control-plane surface exposed to MCP tools.
// Implementations typically wrap the Wails App / session.Manager.
type Backend interface {
	SessionState() session.State
	ReloadContexts() (cluster.ClusterInventory, error)
	ProbeContext(ctx context.Context, contextName string) (cluster.ProbeResult, error)
	Namespaces(ctx context.Context, contextName string) ([]string, error)
	ListServices(ctx context.Context, contextName, namespace string) ([]cluster.ServiceInfo, error)
	ListPods(ctx context.Context, contextName, namespace string) ([]cluster.PodInfo, error)
	Connect(ctx context.Context, contextName, namespace string) error
	Disconnect() error
	GetManualNetwork(contextName string) cluster.ManualNetwork
	SetManualNetwork(contextName string, network cluster.ManualNetwork) error
	GetHostAliases(contextName string) []store.HostAliasSpec
	SetHostAliases(contextName string, items []store.HostAliasSpec) error
	StartIntercept(ctx context.Context, mapping intercept.Mapping) (intercept.Info, error)
	StartMirror(ctx context.Context, mapping intercept.Mapping) (intercept.Info, error)
	StopIntercept(ctx context.Context, id string) error
	ListIntercepts() []intercept.Info
	ListMirrors() []intercept.Info
	StartPreview(ctx context.Context, request intercept.PreviewRequest) (intercept.Info, error)
	StopPreview(ctx context.Context, id string) error
	ListPreviews() []intercept.Info
	StartPortForward(ctx context.Context, request portfwd.Request) (portfwd.Info, error)
	StopPortForward(id string) error
	ListPortForwards() []portfwd.Info
	HelperStatus(ctx context.Context) helper.Status
	InstallHelper(ctx context.Context) error
	UninstallHelper(ctx context.Context) error
	SingBoxConfig() ([]byte, error)
	ExecPodCommand(ctx context.Context, request PodCommandRequest) (PodCommandResult, error)
	StartFileTransfer(ctx context.Context, request filemanager.TransferRequest) (filemanager.TransferTask, error)
	ListFileTransfers() []filemanager.TransferTask
	CancelFileTransfer(id string) error
}
