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
	LocalPort  uint16 `json:"localPort,omitempty"`
}

// TrafficBindingSession is a read-only projection of one TrafficBinding CRD.
// It intentionally contains no database Task state.
type TrafficBindingSession struct {
	ID           string                 `json:"id"`
	Name         string                 `json:"name"`
	Namespace    string                 `json:"namespace"`
	SessionID    string                 `json:"sessionId"`
	Mode         string                 `json:"mode"`
	DesiredState string                 `json:"desiredState"`
	Phase        string                 `json:"phase"`
	Target       *TrafficBindingTarget  `json:"target,omitempty"`
	Preview      *TrafficBindingPreview `json:"preview,omitempty"`
	Relay        *TrafficBindingRelay   `json:"relay,omitempty"`
	Ports        []TrafficBindingPort   `json:"ports"`
	ServiceName  string                 `json:"serviceName,omitempty"`
	ClusterIP    string                 `json:"serviceClusterIp,omitempty"`

	CreatedAt time.Time `json:"createdAt" ts_type:"string"`
}

type TrafficBindingTarget struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

type TrafficBindingPreview struct {
	ServiceName string `json:"serviceName"`
}

type TrafficBindingRelay struct {
	Address string `json:"address"`
}

type TrafficBindingPort struct {
	Name       string `json:"name,omitempty"`
	TargetPort int32  `json:"targetPort"`
	RelayPort  *int32 `json:"relayPort,omitempty"`
	Protocol   string `json:"protocol"`
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
	LocalPort   uint16           `json:"localPort,omitempty"`
	DialAddress string           `json:"dialAddress"`
	CreatedAt   time.Time        `json:"createdAt"           ts_type:"string"`
	UpdatedAt   time.Time        `json:"updatedAt"           ts_type:"string"`
	ExpiresAt   time.Time        `json:"expiresAt"           ts_type:"string"`
}

// LocalTarget captures the desktop-local endpoint mapping for one service port.
// It is sent on Create and persisted in the task spec so an active session can
// be restored (reverse-relay reopened) after reconnect.
type LocalTarget struct {
	Protocol    string `json:"protocol"`
	ServicePort int32  `json:"servicePort"`
	LocalHost   string `json:"localHost,omitempty"`
	LocalPort   uint16 `json:"localPort"`
}

type ExchangePort struct {
	Name        string `json:"name,omitempty"`
	ServicePort int32  `json:"servicePort"`
	Protocol    string `json:"protocol"`
}

type ExchangeSpec struct {
	Service      string         `json:"service"`
	Ports        []ExchangePort `json:"ports"`
	LocalTargets []LocalTarget  `json:"localTargets,omitempty"`
}

type ExchangeTask struct {
	ID           string           `json:"id"`
	SessionID    string           `json:"sessionId"`
	Namespace    string           `json:"namespace"`
	State        remotetask.State `json:"state"`
	Service      string           `json:"service"`
	ClusterIP    string           `json:"clusterIp"`
	Ports        []ExchangePort   `json:"ports"`
	LocalTargets []LocalTarget    `json:"localTargets,omitempty"`
	CreatedAt    time.Time        `json:"createdAt"              ts_type:"string"`
	UpdatedAt    time.Time        `json:"updatedAt"              ts_type:"string"`
	ExpiresAt    time.Time        `json:"expiresAt"              ts_type:"string"`
}

type MirrorPort struct {
	Name        string `json:"name,omitempty"`
	ServicePort int32  `json:"servicePort"`
	Protocol    string `json:"protocol"`
}

type MirrorSpec struct {
	Service      string        `json:"service"`
	Ports        []MirrorPort  `json:"ports"`
	LocalTargets []LocalTarget `json:"localTargets,omitempty"`
}

type MirrorTask struct {
	ID           string           `json:"id"`
	SessionID    string           `json:"sessionId"`
	Namespace    string           `json:"namespace"`
	State        remotetask.State `json:"state"`
	Service      string           `json:"service"`
	ClusterIP    string           `json:"clusterIp"`
	Ports        []MirrorPort     `json:"ports"`
	LocalTargets []LocalTarget    `json:"localTargets,omitempty"`
	CreatedAt    time.Time        `json:"createdAt"              ts_type:"string"`
	UpdatedAt    time.Time        `json:"updatedAt"              ts_type:"string"`
	ExpiresAt    time.Time        `json:"expiresAt"              ts_type:"string"`
}

type PreviewPort struct {
	Name        string `json:"name,omitempty"`
	ServicePort int32  `json:"servicePort"`
	Protocol    string `json:"protocol"`
}

type PreviewSpec struct {
	Name         string        `json:"name"`
	Ports        []PreviewPort `json:"ports"`
	LocalTargets []LocalTarget `json:"localTargets,omitempty"`
}

type PreviewTask struct {
	ID           string           `json:"id"`
	SessionID    string           `json:"sessionId"`
	Namespace    string           `json:"namespace"`
	State        remotetask.State `json:"state"`
	Name         string           `json:"name"`
	ClusterIP    string           `json:"clusterIp,omitempty"`
	Ports        []PreviewPort    `json:"ports"`
	LocalTargets []LocalTarget    `json:"localTargets,omitempty"`
	CreatedAt    time.Time        `json:"createdAt"              ts_type:"string"`
	UpdatedAt    time.Time        `json:"updatedAt"              ts_type:"string"`
}
