package mcp

import (
	"context"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	clientfiletransfer "github.com/fengqi-dev/kube-loop/internal/client/filetransfer"
	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
)

const (
	actionConnect    = "connect"
	actionCreate     = "create"
	actionDelete     = "delete"
	actionDisconnect = "disconnect"
	actionList       = "list"
	actionRename     = "rename"
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

type manageClusterIn struct {
	Action    string `json:"action"              jsonschema:"get or list"`
	Type      string `json:"type"                jsonschema:"version, capabilities, namespace, service, or pod"`
	ProfileID string `json:"profileId"           jsonschema:"Explicit active Server Profile ID"`
	Namespace string `json:"namespace,omitempty" jsonschema:"Explicit namespace for capabilities, Services, or Pods"`
}

type manageClusterOut struct {
	Action       string                     `json:"action"`
	Type         string                     `json:"type"`
	ProfileID    string                     `json:"profileId"`
	Namespace    string                     `json:"namespace,omitempty"`
	Version      *clientremote.Version      `json:"version,omitempty"`
	Capabilities *clientremote.Capabilities `json:"capabilities,omitempty"`
	Namespaces   []clientremote.Namespace   `json:"namespaces,omitempty"`
	Services     []clientremote.Service     `json:"services,omitempty"`
	Pods         []clientremote.Pod         `json:"pods,omitempty"`
}

type manageConnectionIn struct {
	Action    string `json:"action"              jsonschema:"status, connect, or disconnect"`
	ProfileID string `json:"profileId"           jsonschema:"Explicit active Server Profile ID"`
	SessionID string `json:"sessionId,omitempty" jsonschema:"Explicit active Session ID required for disconnect"`
	Namespace string `json:"namespace,omitempty" jsonschema:"Explicit namespace required for connect and disconnect"`
}

type manageConnectionOut struct {
	Action    string               `json:"action"`
	ProfileID string               `json:"profileId"`
	Session   clientremote.Session `json:"session"`
}

type portMappingIn struct {
	ServicePort int32  `json:"servicePort" jsonschema:"Service port number"`
	Protocol    string `json:"protocol"    jsonschema:"tcp or udp"`
	LocalHost   string `json:"localHost"   jsonschema:"Explicit local target host"`
	LocalPort   uint16 `json:"localPort"   jsonschema:"Explicit local target port"`
}

type manageTrafficIn struct {
	Action     string          `json:"action"               jsonschema:"start, stop, or list"`
	Type       string          `json:"type,omitempty"       jsonschema:"Traffic type; optional only for list"`
	ProfileID  string          `json:"profileId"            jsonschema:"Explicit active Server Profile ID"`
	SessionID  string          `json:"sessionId,omitempty"  jsonschema:"Active Session ID for start and stop"`
	Namespace  string          `json:"namespace,omitempty"  jsonschema:"Active namespace for start and stop"`
	TaskID     string          `json:"taskId,omitempty"     jsonschema:"Explicit Task ID required for stop"`
	Service    string          `json:"service,omitempty"    jsonschema:"Service name for exchange or mirror"`
	Name       string          `json:"name,omitempty"       jsonschema:"Preview Service name"`
	Targets    []portMappingIn `json:"targets,omitempty"    jsonschema:"Local targets for exchange, mirror, or preview"`
	TargetKind string          `json:"targetKind,omitempty" jsonschema:"pod or service for port_forward"`
	TargetName string          `json:"targetName,omitempty" jsonschema:"Explicit Pod or Service name for port_forward"`
	Protocol   string          `json:"protocol,omitempty"   jsonschema:"tcp or udp for port_forward"`
	RemotePort uint16          `json:"remotePort,omitempty" jsonschema:"Explicit remote port for port_forward"`
	LocalPort  uint16          `json:"localPort,omitempty"  jsonschema:"Local listen port; 0 allocates an ephemeral port"`
}

type manageTrafficOut struct {
	Action string        `json:"action"`
	Type   string        `json:"type,omitempty"`
	TaskID string        `json:"taskId,omitempty"`
	Item   *TrafficItem  `json:"item,omitempty"`
	Items  []TrafficItem `json:"items,omitempty"`
}

type podCommandIn struct {
	ProfileID      string   `json:"profileId"                jsonschema:"Explicit active Server Profile ID"`
	SessionID      string   `json:"sessionId"                jsonschema:"Explicit active Session ID"`
	Namespace      string   `json:"namespace"                jsonschema:"Explicit active namespace"`
	Pod            string   `json:"pod"                      jsonschema:"Explicit Pod name"`
	Container      string   `json:"container,omitempty"      jsonschema:"Container name; omit for server default"`
	Command        []string `json:"command"                  jsonschema:"Exact argv; no implicit shell is added"`
	TimeoutSeconds int      `json:"timeoutSeconds,omitempty" jsonschema:"1-300; defaults to 30"`
}

type manageFileTransferIn struct {
	Action     string `json:"action"               jsonschema:"start, list, or cancel"`
	ProfileID  string `json:"profileId"            jsonschema:"Explicit active Server Profile ID"`
	SessionID  string `json:"sessionId,omitempty"  jsonschema:"Explicit active Session ID required for start and cancel"`
	Namespace  string `json:"namespace,omitempty"  jsonschema:"Explicit active namespace required for start and cancel"`
	TaskID     string `json:"taskId,omitempty"     jsonschema:"Explicit transfer Task ID required for cancel"`
	Direction  string `json:"direction,omitempty"  jsonschema:"upload or download"`
	Kind       string `json:"kind,omitempty"       jsonschema:"file or directory"`
	Pod        string `json:"pod,omitempty"        jsonschema:"Explicit Pod name"`
	Container  string `json:"container,omitempty"  jsonschema:"Explicit container name when needed"`
	LocalPath  string `json:"localPath,omitempty"  jsonschema:"Explicit absolute local path"`
	RemotePath string `json:"remotePath,omitempty" jsonschema:"Explicit absolute container path"`
	Overwrite  bool   `json:"overwrite,omitempty"  jsonschema:"Whether an existing destination may be replaced"`
}

type manageFileTransferOut struct {
	Action string                    `json:"action"`
	TaskID string                    `json:"taskId,omitempty"`
	Task   *clientfiletransfer.Task  `json:"task,omitempty"`
	Items  []clientfiletransfer.Task `json:"items,omitempty"`
}

type managePodFilesIn struct {
	Action         string `json:"action"                   jsonschema:"list, create, rename, or delete"`
	ProfileID      string `json:"profileId"                jsonschema:"Explicit active Server Profile ID"`
	SessionID      string `json:"sessionId"                jsonschema:"Explicit active Session ID"`
	Namespace      string `json:"namespace"                jsonschema:"Explicit active namespace"`
	Pod            string `json:"pod"                      jsonschema:"Explicit Pod name"`
	Container      string `json:"container,omitempty"      jsonschema:"Explicit container name when needed"`
	Path           string `json:"path"                     jsonschema:"Explicit absolute container path"`
	Destination    string `json:"destination,omitempty"    jsonschema:"Explicit absolute destination path for rename"`
	Kind           string `json:"kind,omitempty"           jsonschema:"file or directory for create"`
	Recursive      bool   `json:"recursive,omitempty"      jsonschema:"Whether directory deletion is recursive"`
	IdempotencyKey string `json:"idempotencyKey,omitempty" jsonschema:"Unique key for create, rename, and delete"`
}

type managePodFilesOut struct {
	Action    string                    `json:"action"`
	ProfileID string                    `json:"profileId"`
	SessionID string                    `json:"sessionId"`
	Namespace string                    `json:"namespace"`
	Listing   *clientremote.PodFileList `json:"listing,omitempty"`
	Task      *clientremote.PodFileTask `json:"task,omitempty"`
}

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
		Description: "Start, stop, or list Exchange, Mirror, Preview, and Port Forward Tasks. " +
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

func manageCluster(ctx context.Context, backend Backend, input manageClusterIn) (manageClusterOut, error) {
	input.Action, input.Type = strings.TrimSpace(input.Action), strings.TrimSpace(input.Type)
	output := manageClusterOut{
		Action: input.Action, Type: input.Type, ProfileID: strings.TrimSpace(input.ProfileID),
		Namespace: strings.TrimSpace(input.Namespace),
	}
	if output.ProfileID == "" {
		return manageClusterOut{}, invalid(fieldProfileID, "profileId is required")
	}
	switch {
	case input.Action == "get" && input.Type == "version":
		value, err := backend.Version(ctx, output.ProfileID)
		output.Version = &value
		return output, err
	case input.Action == "get" && input.Type == "capabilities":
		if output.Namespace == "" {
			return manageClusterOut{}, invalid(resourceNamespace, "namespace is required for capabilities")
		}
		value, err := backend.Capabilities(ctx, output.ProfileID, output.Namespace)
		output.Capabilities = &value
		return output, err
	case input.Action == actionList && input.Type == resourceNamespace:
		items, err := backend.Namespaces(ctx, output.ProfileID)
		output.Namespaces = items
		return output, err
	case input.Action == actionList && input.Type == "service":
		if output.Namespace == "" {
			return manageClusterOut{}, invalid(resourceNamespace, "namespace is required for Services")
		}
		items, err := backend.Services(ctx, output.ProfileID, output.Namespace)
		output.Services = items
		return output, err
	case input.Action == actionList && input.Type == resourcePod:
		if output.Namespace == "" {
			return manageClusterOut{}, invalid(resourceNamespace, "namespace is required for Pods")
		}
		items, err := backend.Pods(ctx, output.ProfileID, output.Namespace)
		output.Pods = items
		return output, err
	default:
		return manageClusterOut{}, invalid(
			"action",
			"supported combinations are get/version, get/capabilities, and list/namespace|service|pod",
		)
	}
}

func manageConnection(ctx context.Context, backend Backend, input manageConnectionIn) (manageConnectionOut, error) {
	input.Action, input.ProfileID = strings.TrimSpace(input.Action), strings.TrimSpace(input.ProfileID)
	if input.ProfileID == "" {
		return manageConnectionOut{}, invalid("profileId", "profileId is required")
	}
	output := manageConnectionOut{Action: input.Action, ProfileID: input.ProfileID}
	var (
		session clientremote.Session
		err     error
	)
	switch input.Action {
	case "status":
		session, err = backend.CurrentSession(input.ProfileID)
	case actionConnect:
		if strings.TrimSpace(input.Namespace) == "" {
			return manageConnectionOut{}, invalid(resourceNamespace, "namespace is required for connect")
		}
		session, err = backend.Connect(ctx, input.ProfileID, input.Namespace)
	case actionDisconnect:
		if strings.TrimSpace(input.SessionID) == "" {
			return manageConnectionOut{}, invalid("sessionId", "sessionId is required for disconnect")
		}
		if strings.TrimSpace(input.Namespace) == "" {
			return manageConnectionOut{}, invalid(resourceNamespace, "namespace is required for disconnect")
		}
		current, currentErr := backend.CurrentSession(input.ProfileID)
		if currentErr != nil {
			return manageConnectionOut{}, currentErr
		}
		err = backend.Disconnect(ctx, input.ProfileID, input.SessionID, input.Namespace)
		session = current
		if err == nil {
			session.State = sessionStateStopped
		}
	default:
		return manageConnectionOut{}, invalid("action", "action must be status, connect, or disconnect")
	}
	output.Session = session
	return output, err
}

func manageTraffic(ctx context.Context, backend Backend, input manageTrafficIn) (manageTrafficOut, error) {
	input.Action, input.Type = strings.TrimSpace(input.Action), strings.TrimSpace(input.Type)
	input.ProfileID = strings.TrimSpace(input.ProfileID)
	if input.ProfileID == "" {
		return manageTrafficOut{}, invalid("profileId", "profileId is required")
	}
	switch input.Action {
	case actionList:
		if input.Type != "" && !validTrafficType(input.Type) {
			return manageTrafficOut{}, invalid("type", "type must be exchange, mirror, preview, or port_forward")
		}
		items, err := backend.ListTraffic(input.ProfileID, input.Type)
		return manageTrafficOut{Action: input.Action, Type: input.Type, Items: items}, err
	case actionStart:
		if err := validateMutationIdentity(input.ProfileID, input.SessionID, input.Namespace); err != nil {
			return manageTrafficOut{}, err
		}
		if !validTrafficType(input.Type) {
			return manageTrafficOut{}, invalid("type", "type must be exchange, mirror, preview, or port_forward")
		}
		switch input.Type {
		case trafficTypeExchange, trafficTypeMirror:
			if strings.TrimSpace(input.Service) == "" {
				return manageTrafficOut{}, invalid("service", "service is required")
			}
			if len(input.Targets) == 0 {
				return manageTrafficOut{}, invalid("targets", "at least one explicit local target is required")
			}
		case trafficTypePreview:
			if strings.TrimSpace(input.Name) == "" {
				return manageTrafficOut{}, invalid("name", "name is required")
			}
			if len(input.Targets) == 0 {
				return manageTrafficOut{}, invalid("targets", "at least one explicit local target is required")
			}
		default:
			if input.TargetKind != resourcePod && input.TargetKind != "service" {
				return manageTrafficOut{}, invalid("targetKind", "targetKind must be pod or service")
			}
			if strings.TrimSpace(input.TargetName) == "" {
				return manageTrafficOut{}, invalid("targetName", "targetName is required")
			}
			if input.RemotePort == 0 {
				return manageTrafficOut{}, invalid("remotePort", "remotePort is required")
			}
		}
		targets := make([]LocalTarget, len(input.Targets))
		for index, target := range input.Targets {
			targets[index] = LocalTarget(target)
		}
		item, err := backend.StartTraffic(ctx, TrafficStartRequest{
			Type:       input.Type,
			ProfileID:  input.ProfileID,
			SessionID:  input.SessionID,
			Namespace:  input.Namespace,
			Service:    input.Service,
			Name:       input.Name,
			Targets:    targets,
			TargetKind: input.TargetKind,
			TargetName: input.TargetName,
			Protocol:   input.Protocol,
			RemotePort: input.RemotePort,
			LocalPort:  input.LocalPort,
		})
		return manageTrafficOut{Action: input.Action, Type: input.Type, TaskID: trafficItemID(item), Item: &item}, err
	case "stop":
		if err := validateMutationIdentity(input.ProfileID, input.SessionID, input.Namespace); err != nil {
			return manageTrafficOut{}, err
		}
		if !validTrafficType(input.Type) {
			return manageTrafficOut{}, invalid("type", "type must be exchange, mirror, preview, or port_forward")
		}
		if strings.TrimSpace(input.TaskID) == "" {
			return manageTrafficOut{}, invalid("taskId", "taskId is required")
		}
		err := backend.StopTraffic(ctx, TrafficIdentity{
			Type: input.Type, ProfileID: input.ProfileID, SessionID: input.SessionID,
			Namespace: input.Namespace, TaskID: input.TaskID,
		})
		return manageTrafficOut{Action: input.Action, Type: input.Type, TaskID: input.TaskID}, err
	default:
		return manageTrafficOut{}, invalid("action", "action must be start, stop, or list")
	}
}

func execPodCommand(ctx context.Context, backend Backend, input podCommandIn) (PodCommandResult, error) {
	if err := validateMutationIdentity(input.ProfileID, input.SessionID, input.Namespace); err != nil {
		return PodCommandResult{}, err
	}
	if strings.TrimSpace(input.Pod) == "" {
		return PodCommandResult{}, invalid(resourcePod, "pod is required")
	}
	if len(input.Command) == 0 {
		return PodCommandResult{}, invalid("command", "command must contain explicit argv")
	}
	return backend.ExecPodCommand(ctx, PodCommandRequest{
		ProfileID: input.ProfileID, SessionID: input.SessionID, Namespace: input.Namespace,
		Pod: input.Pod, Container: input.Container, Command: append([]string(nil), input.Command...),
		TimeoutSeconds: input.TimeoutSeconds,
	})
}

func manageFileTransfer(backend Backend, input manageFileTransferIn) (manageFileTransferOut, error) {
	input.Action, input.ProfileID = strings.TrimSpace(input.Action), strings.TrimSpace(input.ProfileID)
	if input.ProfileID == "" {
		return manageFileTransferOut{}, invalid("profileId", "profileId is required")
	}
	switch input.Action {
	case actionList:
		items, err := backend.ListFileTransfers(input.ProfileID)
		return manageFileTransferOut{Action: input.Action, Items: items}, err
	case actionStart:
		if err := validateMutationIdentity(input.ProfileID, input.SessionID, input.Namespace); err != nil {
			return manageFileTransferOut{}, err
		}
		if input.Direction != "upload" && input.Direction != "download" {
			return manageFileTransferOut{}, invalid("direction", "direction must be upload or download")
		}
		if input.Kind != fileKindFile && input.Kind != fileKindDirectory {
			return manageFileTransferOut{}, invalid("kind", "kind must be file or directory")
		}
		if strings.TrimSpace(input.Pod) == "" {
			return manageFileTransferOut{}, invalid(resourcePod, "pod is required")
		}
		if strings.TrimSpace(input.LocalPath) == "" {
			return manageFileTransferOut{}, invalid("localPath", "localPath is required")
		}
		if strings.TrimSpace(input.RemotePath) == "" {
			return manageFileTransferOut{}, invalid("remotePath", "remotePath is required")
		}
		task, err := backend.StartFileTransfer(TrafficIdentity{
			ProfileID: input.ProfileID, SessionID: input.SessionID, Namespace: input.Namespace,
		}, clientfiletransfer.Request{
			ProfileID: input.ProfileID, Direction: input.Direction, Kind: input.Kind,
			Pod: input.Pod, Container: input.Container, LocalPath: input.LocalPath,
			RemotePath: input.RemotePath, Overwrite: input.Overwrite,
		})
		return manageFileTransferOut{Action: input.Action, TaskID: task.ID, Task: &task}, err
	case "cancel":
		if err := validateMutationIdentity(input.ProfileID, input.SessionID, input.Namespace); err != nil {
			return manageFileTransferOut{}, err
		}
		if strings.TrimSpace(input.TaskID) == "" {
			return manageFileTransferOut{}, invalid("taskId", "taskId is required")
		}
		err := backend.CancelFileTransfer(TrafficIdentity{
			ProfileID: input.ProfileID, SessionID: input.SessionID,
			Namespace: input.Namespace, TaskID: input.TaskID,
		})
		return manageFileTransferOut{Action: input.Action, TaskID: input.TaskID}, err
	default:
		return manageFileTransferOut{}, invalid("action", "action must be start, list, or cancel")
	}
}

func managePodFiles(ctx context.Context, backend Backend, input managePodFilesIn) (managePodFilesOut, error) {
	input.Action = strings.ToLower(strings.TrimSpace(input.Action))
	identity := TrafficIdentity{
		ProfileID: strings.TrimSpace(input.ProfileID),
		SessionID: strings.TrimSpace(input.SessionID),
		Namespace: strings.TrimSpace(input.Namespace),
	}
	if err := validateMutationIdentity(identity.ProfileID, identity.SessionID, identity.Namespace); err != nil {
		return managePodFilesOut{}, err
	}
	input.Pod = strings.TrimSpace(input.Pod)
	input.Container = strings.TrimSpace(input.Container)
	input.Path = strings.TrimSpace(input.Path)
	if input.Pod == "" {
		return managePodFilesOut{}, invalid(resourcePod, "pod is required")
	}
	if input.Path == "" {
		return managePodFilesOut{}, invalid("path", "path is required")
	}
	output := managePodFilesOut{
		Action: input.Action, ProfileID: identity.ProfileID,
		SessionID: identity.SessionID, Namespace: identity.Namespace,
	}
	spec := clientremote.PodFileSpec{
		Pod: input.Pod, Container: input.Container, Path: input.Path,
		Destination: strings.TrimSpace(input.Destination),
		Kind:        strings.ToLower(strings.TrimSpace(input.Kind)), Recursive: input.Recursive,
	}
	if input.Action == actionList {
		listing, err := backend.ListPodFiles(ctx, identity, spec)
		output.Listing = &listing
		return output, err
	}
	if input.Action != actionCreate && input.Action != actionRename && input.Action != actionDelete {
		return managePodFilesOut{}, invalid("action", "action must be list, create, rename, or delete")
	}
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.IdempotencyKey == "" || len(input.IdempotencyKey) > 128 {
		return managePodFilesOut{}, invalid(
			"idempotencyKey",
			"idempotencyKey is required and must be at most 128 bytes",
		)
	}
	if input.Action == actionCreate && spec.Kind != fileKindFile && spec.Kind != fileKindDirectory {
		return managePodFilesOut{}, invalid("kind", "kind must be file or directory for create")
	}
	if input.Action == actionRename && spec.Destination == "" {
		return managePodFilesOut{}, invalid("destination", "destination is required for rename")
	}
	task, err := backend.CreatePodFileOperation(ctx, identity, input.Action, spec, input.IdempotencyKey)
	output.Task = &task
	return output, err
}

func validateMutationIdentity(profileID, sessionID, namespace string) error {
	if strings.TrimSpace(profileID) == "" {
		return invalid("profileId", "profileId is required")
	}
	if strings.TrimSpace(sessionID) == "" {
		return invalid("sessionId", "sessionId is required")
	}
	if strings.TrimSpace(namespace) == "" {
		return invalid(resourceNamespace, "namespace is required")
	}
	return nil
}

func validTrafficType(value string) bool {
	return value == trafficTypeExchange || value == trafficTypeMirror ||
		value == trafficTypePreview || value == trafficTypePortForward
}
