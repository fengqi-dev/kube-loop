package mcp

import (
	clientfiletransfer "github.com/fengqi-dev/kube-loop/internal/client/filetransfer"
	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
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
	Action     string          `json:"action"               jsonschema:"start, pause, resume, delete, or list"`
	Type       string          `json:"type,omitempty"       jsonschema:"Traffic type; optional only for list"`
	ProfileID  string          `json:"profileId"            jsonschema:"Explicit active Server Profile ID"`
	SessionID  string          `json:"sessionId,omitempty"  jsonschema:"Active Session ID for mutations"`
	Namespace  string          `json:"namespace,omitempty"  jsonschema:"Active namespace for mutations"`
	TaskID     string          `json:"taskId,omitempty"     jsonschema:"Explicit Task ID required for pause, resume, and delete"`
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
