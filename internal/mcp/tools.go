package mcp

import (
	"context"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	actionConnect    = "connect"
	actionCreate     = "create"
	actionDelete     = "delete"
	actionDisconnect = "disconnect"
	actionList       = "list"
	actionPause      = "pause"
	actionRename     = "rename"
	actionResume     = "resume"
	actionStart      = "start"

	fileKindDirectory       = "directory"
	fileKindFile            = "file"
	fileTransferUnavailable = "file transfer is unavailable"
	fieldProfileID          = "profileId"
	resourceNamespace       = "namespace"
	resourcePod             = "pod"
	sessionStateActive      = "active"
	sessionStateStopped     = "stopped"

	trafficTypeExchange    = "exchange"
	trafficTypeMirror      = "mirror"
	trafficTypePortForward = "port_forward"
	trafficTypePreview     = "preview"

	toolExecPodCommand     = "exec_pod_command"
	toolManageCluster      = "manage_cluster"
	toolManageConnection   = "manage_connection"
	toolManageFileTransfer = "manage_file_transfer"
	toolManagePodFiles     = "manage_pod_files"
	toolManageTraffic      = "manage_traffic"
)

func registerTools(server *mcpsdk.Server, backend Backend) {
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: toolManageCluster,
		Description: "Read Kubernetes resources through the authenticated Control Plane client. " +
			"profileId is always explicit; namespace is required for capabilities, Services, and Pods.",
	}, func(
		ctx context.Context,
		_ *mcpsdk.CallToolRequest,
		input manageClusterIn,
	) (*mcpsdk.CallToolResult, manageClusterOut, error) {
		output, err := manageCluster(ctx, backend, input)
		return nil, output, stableError(err)
	})

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: toolManageConnection,
		Description: "Inspect, connect, or disconnect the active Cluster Session. " +
			"Disconnect requires the exact profileId, sessionId, and namespace returned by status/connect.",
	}, func(
		ctx context.Context,
		_ *mcpsdk.CallToolRequest,
		input manageConnectionIn,
	) (*mcpsdk.CallToolResult, manageConnectionOut, error) {
		output, err := manageConnection(ctx, backend, input)
		return nil, output, stableError(err)
	})

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: toolManageTraffic,
		Description: "Start, pause, resume, delete, or list Exchange, Mirror, Preview, and Port Forward Tasks. " +
			"Every mutation requires exact Profile, Session, namespace, target, and local endpoint parameters.",
	}, func(
		ctx context.Context,
		_ *mcpsdk.CallToolRequest,
		input manageTrafficIn,
	) (*mcpsdk.CallToolResult, manageTrafficOut, error) {
		output, err := manageTraffic(ctx, backend, input)
		return nil, output, stableError(err)
	})

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: toolExecPodCommand,
		Description: "Execute an exact argv in a Pod through the authenticated Control Plane exec stream. " +
			"No shell is inferred; stdoutBase64 and stderrBase64 are capped at 1 MiB before encoding.",
	}, func(
		ctx context.Context,
		_ *mcpsdk.CallToolRequest,
		input podCommandIn,
	) (*mcpsdk.CallToolResult, PodCommandResult, error) {
		output, err := execPodCommand(ctx, backend, input)
		return nil, output, stableError(err)
	})

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: toolManageFileTransfer,
		Description: "Start, list, or cancel file transfers. Mutations require the exact active Session " +
			"and explicit localPath, remotePath, direction, kind, Pod, container, and overwrite choice.",
	}, func(
		_ context.Context,
		_ *mcpsdk.CallToolRequest,
		input manageFileTransferIn,
	) (*mcpsdk.CallToolResult, manageFileTransferOut, error) {
		output, err := manageFileTransfer(backend, input)
		return nil, output, stableError(err)
	})

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: toolManagePodFiles,
		Description: "List, create, rename, or delete files and directories in a Pod through the " +
			"authenticated Control Plane. Every call requires the exact active Profile, Session, namespace, " +
			"Pod, container path, and explicit mutation parameters.",
	}, func(
		ctx context.Context,
		_ *mcpsdk.CallToolRequest,
		input managePodFilesIn,
	) (*mcpsdk.CallToolResult, managePodFilesOut, error) {
		output, err := managePodFiles(ctx, backend, input)
		return nil, output, stableError(err)
	})
}
