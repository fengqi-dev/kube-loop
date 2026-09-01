package service

import (
	"net"
	"strconv"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

// Spec is the normalized request consumed by the Port Forward service.
type Spec struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Protocol   string `json:"protocol"`
	RemotePort uint16 `json:"remotePort"`
	LocalPort  uint16 `json:"localPort,omitempty"`
}

// Target is the resolved in-cluster dial target.
type Target struct {
	Host string `json:"host"`
	Port uint16 `json:"port"`
}

func (target Target) Address() string {
	return net.JoinHostPort(target.Host, strconv.Itoa(int(target.Port)))
}

// PortForward is the API projection of a TrafficBinding-backed Session.
type PortForward struct {
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
	CreatedAt   time.Time        `json:"createdAt"`
	UpdatedAt   time.Time        `json:"updatedAt"`
	ExpiresAt   time.Time        `json:"expiresAt"`
}
