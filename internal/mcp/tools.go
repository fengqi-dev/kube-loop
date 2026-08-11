package mcp

import (
	"context"
	"strings"

	clientfiletransfer "github.com/fengqi-dev/kube-loop/internal/client/filetransfer"
	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type manageClusterIn struct {
	Action    string `json:"action" jsonschema:"get or list"`
	Type      string `json:"type" jsonschema:"version, capabilities, namespace, service, or pod"`
	ProfileID string `json:"profileId" jsonschema:"Explicit active Server Profile ID"`
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
	Action    string `json:"action" jsonschema:"status, connect, or disconnect"`
	ProfileID string `json:"profileId" jsonschema:"Explicit active Server Profile ID"`
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
	Protocol    string `json:"protocol" jsonschema:"tcp or udp"`
	LocalHost   string `json:"localHost" jsonschema:"Explicit local target host"`
	LocalPort   uint16 `json:"localPort" jsonschema:"Explicit local target port"`
}

type manageTrafficIn struct {
	Action     string          `json:"action" jsonschema:"start, stop, or list"`
	Type       string          `json:"type,omitempty" jsonschema:"exchange, mirror, preview, or port_forward; optional only for list"`
	ProfileID  string          `json:"profileId" jsonschema:"Explicit active Server Profile ID"`
	SessionID  string          `json:"sessionId,omitempty" jsonschema:"Explicit active Session ID required for start and stop"`
	Namespace  string          `json:"namespace,omitempty" jsonschema:"Explicit active namespace required for start and stop"`
	TaskID     string          `json:"taskId,omitempty" jsonschema:"Explicit Task ID required for stop"`
	Service    string          `json:"service,omitempty" jsonschema:"Service name for exchange or mirror"`
	Name       string          `json:"name,omitempty" jsonschema:"Preview Service name"`
	Targets    []portMappingIn `json:"targets,omitempty" jsonschema:"Explicit local targets for exchange, mirror, or preview"`
	TargetKind string          `json:"targetKind,omitempty" jsonschema:"pod or service for port_forward"`
	TargetName string          `json:"targetName,omitempty" jsonschema:"Explicit Pod or Service name for port_forward"`
	Protocol   string          `json:"protocol,omitempty" jsonschema:"tcp or udp for port_forward"`
	RemotePort uint16          `json:"remotePort,omitempty" jsonschema:"Explicit remote port for port_forward"`
	LocalPort  uint16          `json:"localPort,omitempty" jsonschema:"Local listen port; 0 allocates an ephemeral port"`
}

type manageTrafficOut struct {
	Action string        `json:"action"`
	Type   string        `json:"type,omitempty"`
	TaskID string        `json:"taskId,omitempty"`
	Item   *TrafficItem  `json:"item,omitempty"`
	Items  []TrafficItem `json:"items,omitempty"`
}

type podCommandIn struct {
	ProfileID      string   `json:"profileId" jsonschema:"Explicit active Server Profile ID"`
	SessionID      string   `json:"sessionId" jsonschema:"Explicit active Session ID"`
	Namespace      string   `json:"namespace" jsonschema:"Explicit active namespace"`
	Pod            string   `json:"pod" jsonschema:"Explicit Pod name"`
	Container      string   `json:"container,omitempty" jsonschema:"Explicit container name; omit only to use Gateway default selection"`
	Command        []string `json:"command" jsonschema:"Exact argv; no implicit shell is added"`
	TimeoutSeconds int      `json:"timeoutSeconds,omitempty" jsonschema:"1-300; defaults to 30"`
}

type manageFileTransferIn struct {
	Action     string `json:"action" jsonschema:"start, list, or cancel"`
	ProfileID  string `json:"profileId" jsonschema:"Explicit active Server Profile ID"`
	SessionID  string `json:"sessionId,omitempty" jsonschema:"Explicit active Session ID required for start and cancel"`
	Namespace  string `json:"namespace,omitempty" jsonschema:"Explicit active namespace required for start and cancel"`
	TaskID     string `json:"taskId,omitempty" jsonschema:"Explicit transfer Task ID required for cancel"`
	Direction  string `json:"direction,omitempty" jsonschema:"upload or download"`
	Kind       string `json:"kind,omitempty" jsonschema:"file or directory"`
	Pod        string `json:"pod,omitempty" jsonschema:"Explicit Pod name"`
	Container  string `json:"container,omitempty" jsonschema:"Explicit container name when needed"`
	LocalPath  string `json:"localPath,omitempty" jsonschema:"Explicit absolute local path"`
	RemotePath string `json:"remotePath,omitempty" jsonschema:"Explicit absolute container path"`
	Overwrite  bool   `json:"overwrite,omitempty" jsonschema:"Whether an existing destination may be replaced"`
}

type manageFileTransferOut struct {
	Action string                    `json:"action"`
	TaskID string                    `json:"taskId,omitempty"`
	Task   *clientfiletransfer.Task  `json:"task,omitempty"`
	Items  []clientfiletransfer.Task `json:"items,omitempty"`
}

func registerTools(server *mcpsdk.Server, backend Backend) {
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: "manage_cluster",
		Description: "Read Kubernetes resources through the authenticated Gateway SDK. " +
			"profileId is always explicit; namespace is required for capabilities, Services, and Pods.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, input manageClusterIn) (*mcpsdk.CallToolResult, manageClusterOut, error) {
		output, err := manageCluster(ctx, backend, input)
		return nil, output, stableError(err)
	})

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: "manage_connection",
		Description: "Inspect, connect, or disconnect the active Gateway Session. " +
			"Disconnect requires the exact profileId, sessionId, and namespace returned by status/connect.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, input manageConnectionIn) (*mcpsdk.CallToolResult, manageConnectionOut, error) {
		output, err := manageConnection(ctx, backend, input)
		return nil, output, stableError(err)
	})

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: "manage_traffic",
		Description: "Start, stop, or list Exchange, Mirror, Preview, and Port Forward Tasks. " +
			"Every mutation requires exact Profile, Session, namespace, target, and local endpoint parameters.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, input manageTrafficIn) (*mcpsdk.CallToolResult, manageTrafficOut, error) {
		output, err := manageTraffic(ctx, backend, input)
		return nil, output, stableError(err)
	})

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: "exec_pod_command",
		Description: "Execute an exact argv in a Pod through the authenticated Gateway exec stream. " +
			"No shell is inferred; stdout and stderr are base64-encoded JSON byte fields and capped at 1 MiB each.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, input podCommandIn) (*mcpsdk.CallToolResult, PodCommandResult, error) {
		output, err := execPodCommand(ctx, backend, input)
		return nil, output, stableError(err)
	})

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: "manage_file_transfer",
		Description: "Start, list, or cancel file transfers. Mutations require the exact active Session " +
			"and explicit localPath, remotePath, direction, kind, Pod, container, and overwrite choice.",
	}, func(_ context.Context, _ *mcpsdk.CallToolRequest, input manageFileTransferIn) (*mcpsdk.CallToolResult, manageFileTransferOut, error) {
		output, err := manageFileTransfer(backend, input)
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
		return manageClusterOut{}, invalid("profileId", "profileId is required")
	}
	switch {
	case input.Action == "get" && input.Type == "version":
		value, err := backend.Version(ctx, output.ProfileID)
		output.Version = &value
		return output, err
	case input.Action == "get" && input.Type == "capabilities":
		if output.Namespace == "" {
			return manageClusterOut{}, invalid("namespace", "namespace is required for capabilities")
		}
		value, err := backend.Capabilities(ctx, output.ProfileID, output.Namespace)
		output.Capabilities = &value
		return output, err
	case input.Action == "list" && input.Type == "namespace":
		items, err := backend.Namespaces(ctx, output.ProfileID)
		output.Namespaces = items
		return output, err
	case input.Action == "list" && input.Type == "service":
		if output.Namespace == "" {
			return manageClusterOut{}, invalid("namespace", "namespace is required for Services")
		}
		items, err := backend.Services(ctx, output.ProfileID, output.Namespace)
		output.Services = items
		return output, err
	case input.Action == "list" && input.Type == "pod":
		if output.Namespace == "" {
			return manageClusterOut{}, invalid("namespace", "namespace is required for Pods")
		}
		items, err := backend.Pods(ctx, output.ProfileID, output.Namespace)
		output.Pods = items
		return output, err
	default:
		return manageClusterOut{}, invalid("action", "supported combinations are get/version, get/capabilities, and list/namespace|service|pod")
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
	case "connect":
		if strings.TrimSpace(input.Namespace) == "" {
			return manageConnectionOut{}, invalid("namespace", "namespace is required for connect")
		}
		session, err = backend.Connect(ctx, input.ProfileID, input.Namespace)
	case "disconnect":
		if strings.TrimSpace(input.SessionID) == "" {
			return manageConnectionOut{}, invalid("sessionId", "sessionId is required for disconnect")
		}
		if strings.TrimSpace(input.Namespace) == "" {
			return manageConnectionOut{}, invalid("namespace", "namespace is required for disconnect")
		}
		current, currentErr := backend.CurrentSession(input.ProfileID)
		if currentErr != nil {
			return manageConnectionOut{}, currentErr
		}
		err = backend.Disconnect(ctx, input.ProfileID, input.SessionID, input.Namespace)
		session = current
		if err == nil {
			session.State = "stopped"
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
	case "list":
		if input.Type != "" && !validTrafficType(input.Type) {
			return manageTrafficOut{}, invalid("type", "type must be exchange, mirror, preview, or port_forward")
		}
		items, err := backend.ListTraffic(input.ProfileID, input.Type)
		return manageTrafficOut{Action: input.Action, Type: input.Type, Items: items}, err
	case "start":
		if err := validateMutationIdentity(input.ProfileID, input.SessionID, input.Namespace); err != nil {
			return manageTrafficOut{}, err
		}
		if !validTrafficType(input.Type) {
			return manageTrafficOut{}, invalid("type", "type must be exchange, mirror, preview, or port_forward")
		}
		if input.Type == "exchange" || input.Type == "mirror" {
			if strings.TrimSpace(input.Service) == "" {
				return manageTrafficOut{}, invalid("service", "service is required")
			}
			if len(input.Targets) == 0 {
				return manageTrafficOut{}, invalid("targets", "at least one explicit local target is required")
			}
		} else if input.Type == "preview" {
			if strings.TrimSpace(input.Name) == "" {
				return manageTrafficOut{}, invalid("name", "name is required")
			}
			if len(input.Targets) == 0 {
				return manageTrafficOut{}, invalid("targets", "at least one explicit local target is required")
			}
		} else {
			if input.TargetKind != "pod" && input.TargetKind != "service" {
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
			Type: input.Type, ProfileID: input.ProfileID, SessionID: input.SessionID, Namespace: input.Namespace,
			Service: input.Service, Name: input.Name, Targets: targets, TargetKind: input.TargetKind,
			TargetName: input.TargetName, Protocol: input.Protocol, RemotePort: input.RemotePort, LocalPort: input.LocalPort,
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
		return PodCommandResult{}, invalid("pod", "pod is required")
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
	case "list":
		items, err := backend.ListFileTransfers(input.ProfileID)
		return manageFileTransferOut{Action: input.Action, Items: items}, err
	case "start":
		if err := validateMutationIdentity(input.ProfileID, input.SessionID, input.Namespace); err != nil {
			return manageFileTransferOut{}, err
		}
		if input.Direction != "upload" && input.Direction != "download" {
			return manageFileTransferOut{}, invalid("direction", "direction must be upload or download")
		}
		if input.Kind != "file" && input.Kind != "directory" {
			return manageFileTransferOut{}, invalid("kind", "kind must be file or directory")
		}
		if strings.TrimSpace(input.Pod) == "" {
			return manageFileTransferOut{}, invalid("pod", "pod is required")
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

func validateMutationIdentity(profileID, sessionID, namespace string) error {
	if strings.TrimSpace(profileID) == "" {
		return invalid("profileId", "profileId is required")
	}
	if strings.TrimSpace(sessionID) == "" {
		return invalid("sessionId", "sessionId is required")
	}
	if strings.TrimSpace(namespace) == "" {
		return invalid("namespace", "namespace is required")
	}
	return nil
}

func validTrafficType(value string) bool {
	return value == "exchange" || value == "mirror" || value == "preview" || value == "port_forward"
}
