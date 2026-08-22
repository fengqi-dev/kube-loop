package remote

import (
	"fmt"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/capability"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

type APIError struct {
	Status    int
	Code      string
	Message   string
	Field     string
	RequestID string
}

func (apiError *APIError) Error() string {
	if apiError == nil {
		return ""
	}
	if apiError.Code != "" {
		return fmt.Sprintf("Gateway request failed (%s): %s", apiError.Code, apiError.Message)
	}
	return fmt.Sprintf("Gateway request returned HTTP %d", apiError.Status)
}

type Version struct {
	GitVersion     string `json:"gitVersion"`
	GatewayVersion string `json:"gatewayVersion"`
}

type Capabilities = capability.Snapshot

type Namespace struct {
	Name   string `json:"name"`
	Status string `json:"status,omitempty"`
}

type Pod struct {
	Name            string    `json:"name"`
	Namespace       string    `json:"namespace"`
	Phase           string    `json:"phase,omitempty"`
	PodIP           string    `json:"podIp,omitempty"`
	NodeName        string    `json:"nodeName,omitempty"`
	Ready           bool      `json:"ready"`
	ReadyContainers int32     `json:"readyContainers"`
	TotalContainers int32     `json:"totalContainers"`
	Restarts        int32     `json:"restarts"`
	AgeSeconds      int64     `json:"ageSeconds"`
	Containers      []string  `json:"containers"`
	Ports           []PodPort `json:"ports"`
}

type PodPort struct {
	Name     string `json:"name,omitempty"`
	Port     int32  `json:"port"`
	Protocol string `json:"protocol"`
}

type Service struct {
	Name         string        `json:"name"`
	Namespace    string        `json:"namespace"`
	Type         string        `json:"type"`
	ClusterIP    string        `json:"clusterIp,omitempty"`
	ExternalName string        `json:"externalName,omitempty"`
	ExternalIPs  []string      `json:"externalIps"`
	AgeSeconds   int64         `json:"ageSeconds"`
	Ports        []ServicePort `json:"ports"`
}

type ServicePort struct {
	Name       string `json:"name,omitempty"`
	Port       int32  `json:"port"`
	Protocol   string `json:"protocol"`
	TargetPort string `json:"targetPort,omitempty"`
}

type Session struct {
	ID              string           `json:"id"`
	Namespace       string           `json:"namespace"`
	State           string           `json:"state"`
	Generation      uint64           `json:"generation"`
	CreatedAt       time.Time        `json:"createdAt"              ts_type:"string"`
	UpdatedAt       time.Time        `json:"updatedAt"              ts_type:"string"`
	LastHeartbeatAt time.Time        `json:"lastHeartbeatAt"        ts_type:"string"`
	ExpiresAt       time.Time        `json:"expiresAt"              ts_type:"string"`
	NetworkSpec     networkspec.Spec `json:"networkSpec"`
	NetworkSpecHash string           `json:"networkSpecHash"`
	Capabilities    *Capabilities    `json:"capabilities,omitempty"`
}

type SessionUpdate struct {
	ProfileID string
	Session   Session
}

type RelayTicket struct {
	TokenType string    `json:"tokenType"`
	Ticket    string    `json:"ticket"`
	ExpiresAt time.Time `json:"expiresAt"          ts_type:"string"`
	DeviceID  string    `json:"deviceId"`
	RelayID   string    `json:"relayId,omitempty"`
	Endpoint  string    `json:"endpoint,omitempty"`
}

type PortForwardSpec struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Protocol   string `json:"protocol"`
	RemotePort uint16 `json:"remotePort"`
}

type PortForwardTask struct {
	ID          string           `json:"id"`
	SessionID   string           `json:"sessionId"`
	Namespace   string           `json:"namespace"`
	State       remotetask.State `json:"state"`
	Kind        string           `json:"kind"`
	Name        string           `json:"name"`
	Protocol    string           `json:"protocol"`
	RemotePort  uint16           `json:"remotePort"`
	DialAddress string           `json:"dialAddress"`
	CreatedAt   time.Time        `json:"createdAt"   ts_type:"string"`
	UpdatedAt   time.Time        `json:"updatedAt"   ts_type:"string"`
	ExpiresAt   time.Time        `json:"expiresAt"   ts_type:"string"`
}

type ExchangePort struct {
	Name        string `json:"name,omitempty"`
	ServicePort int32  `json:"servicePort"`
	Protocol    string `json:"protocol"`
}

type ExchangeSpec struct {
	Service string         `json:"service"`
	Ports   []ExchangePort `json:"ports"`
}

type ExchangeTask struct {
	ID        string           `json:"id"`
	SessionID string           `json:"sessionId"`
	Namespace string           `json:"namespace"`
	State     remotetask.State `json:"state"`
	Service   string           `json:"service"`
	ClusterIP string           `json:"clusterIp"`
	Ports     []ExchangePort   `json:"ports"`
	CreatedAt time.Time        `json:"createdAt" ts_type:"string"`
	UpdatedAt time.Time        `json:"updatedAt" ts_type:"string"`
	ExpiresAt time.Time        `json:"expiresAt" ts_type:"string"`
}

type MirrorPort struct {
	Name        string `json:"name,omitempty"`
	ServicePort int32  `json:"servicePort"`
	Protocol    string `json:"protocol"`
}

type MirrorSpec struct {
	Service string       `json:"service"`
	Ports   []MirrorPort `json:"ports"`
}

type MirrorTask struct {
	ID        string           `json:"id"`
	SessionID string           `json:"sessionId"`
	Namespace string           `json:"namespace"`
	State     remotetask.State `json:"state"`
	Service   string           `json:"service"`
	ClusterIP string           `json:"clusterIp"`
	Ports     []MirrorPort     `json:"ports"`
	CreatedAt time.Time        `json:"createdAt" ts_type:"string"`
	UpdatedAt time.Time        `json:"updatedAt" ts_type:"string"`
	ExpiresAt time.Time        `json:"expiresAt" ts_type:"string"`
}

type PreviewPort struct {
	Name        string `json:"name,omitempty"`
	ServicePort int32  `json:"servicePort"`
	Protocol    string `json:"protocol"`
}

type PreviewSpec struct {
	Name  string        `json:"name"`
	Ports []PreviewPort `json:"ports"`
}

type PreviewTask struct {
	ID        string           `json:"id"`
	SessionID string           `json:"sessionId"`
	Namespace string           `json:"namespace"`
	State     remotetask.State `json:"state"`
	Name      string           `json:"name"`
	ClusterIP string           `json:"clusterIp,omitempty"`
	Ports     []PreviewPort    `json:"ports"`
	CreatedAt time.Time        `json:"createdAt"           ts_type:"string"`
	UpdatedAt time.Time        `json:"updatedAt"           ts_type:"string"`
}

type ExecSpec struct {
	Pod       string   `json:"pod"`
	Container string   `json:"container,omitempty"`
	Command   []string `json:"command"`
	TTY       bool     `json:"tty"`
}

type ExecTask struct {
	ID        string           `json:"id"`
	SessionID string           `json:"sessionId"`
	Namespace string           `json:"namespace"`
	State     remotetask.State `json:"state"`
	Pod       string           `json:"pod"`
	Container string           `json:"container,omitempty"`
	TTY       bool             `json:"tty"`
	CreatedAt time.Time        `json:"createdAt"           ts_type:"string"`
	UpdatedAt time.Time        `json:"updatedAt"           ts_type:"string"`
	ExpiresAt time.Time        `json:"expiresAt"           ts_type:"string"`
}

type FileTransferSpec struct {
	Direction  string `json:"direction"`
	Kind       string `json:"kind"`
	Pod        string `json:"pod"`
	Container  string `json:"container,omitempty"`
	RemotePath string `json:"remotePath"`
	Size       uint64 `json:"size,omitempty"`
	Offset     uint64 `json:"offset,omitempty"`
	Checksum   string `json:"checksum,omitempty"`
	Overwrite  bool   `json:"overwrite,omitempty"`
	ResumeID   string `json:"resumeId,omitempty"`
}

type FileTransferTask struct {
	ID         string           `json:"id"`
	SessionID  string           `json:"sessionId"`
	Namespace  string           `json:"namespace"`
	State      remotetask.State `json:"state"`
	Direction  string           `json:"direction"`
	Kind       string           `json:"kind"`
	Pod        string           `json:"pod"`
	Container  string           `json:"container"`
	RemotePath string           `json:"remotePath"`
	Size       uint64           `json:"size,omitempty"`
	Offset     uint64           `json:"offset,omitempty"`
	Checksum   string           `json:"checksum,omitempty"`
	Overwrite  bool             `json:"overwrite,omitempty"`
	ResumeID   string           `json:"resumeId,omitempty"`
	CreatedAt  time.Time        `json:"createdAt"           ts_type:"string"`
	UpdatedAt  time.Time        `json:"updatedAt"           ts_type:"string"`
	ExpiresAt  time.Time        `json:"expiresAt"           ts_type:"string"`
}

type PodFileSpec struct {
	Pod         string `json:"pod"`
	Container   string `json:"container,omitempty"`
	Path        string `json:"path"`
	Destination string `json:"destination,omitempty"`
	Kind        string `json:"kind,omitempty"`
	Recursive   bool   `json:"recursive,omitempty"`
}

type PodFileEntry struct {
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	Kind       string    `json:"kind"`
	Size       int64     `json:"size"`
	Mode       string    `json:"mode"`
	ModifiedAt time.Time `json:"modifiedAt" ts_type:"string"`
}

type PodFileList struct {
	SessionID string         `json:"sessionId"`
	Namespace string         `json:"namespace"`
	Pod       string         `json:"pod"`
	Container string         `json:"container"`
	Path      string         `json:"path"`
	Items     []PodFileEntry `json:"items"`
}

type PodFileResult struct {
	Completed bool   `json:"completed"`
	Error     string `json:"error,omitempty"`
}

type PodFileTask struct {
	ID          string           `json:"id"`
	SessionID   string           `json:"sessionId"`
	Namespace   string           `json:"namespace"`
	State       remotetask.State `json:"state"`
	Action      string           `json:"action"`
	Pod         string           `json:"pod"`
	Container   string           `json:"container"`
	Path        string           `json:"path"`
	Destination string           `json:"destination,omitempty"`
	Kind        string           `json:"kind,omitempty"`
	Recursive   bool             `json:"recursive,omitempty"`
	Result      PodFileResult    `json:"result"`
	CreatedAt   time.Time        `json:"createdAt"             ts_type:"string"`
	UpdatedAt   time.Time        `json:"updatedAt"             ts_type:"string"`
	ExpiresAt   time.Time        `json:"expiresAt"             ts_type:"string"`
}

type page[T any] struct {
	Items           []T    `json:"items"`
	Continue        string `json:"continue,omitempty"`
	ResourceVersion string `json:"resourceVersion,omitempty"`
}
