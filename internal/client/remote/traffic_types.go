package remote

import (
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

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
