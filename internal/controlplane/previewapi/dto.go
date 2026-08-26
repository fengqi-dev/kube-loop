package previewapi

import (
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/entity"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

type Document struct {
	ID        string           `json:"id"`
	SessionID string           `json:"sessionId"`
	Namespace string           `json:"namespace"`
	State     remotetask.State `json:"state"`
	Name      string           `json:"name"`
	ClusterIP string           `json:"clusterIp,omitempty"`
	Ports     []entity.Port    `json:"ports"`
	CreatedAt time.Time        `json:"createdAt"`
	UpdatedAt time.Time        `json:"updatedAt"`
}

type storedSpec struct {
	Name  string        `json:"name"`
	Ports []entity.Port `json:"ports"`
}

type ownerResult struct {
	OwnerID       string `json:"ownerId"`
	GatewayIP     string `json:"gatewayIp"`
	ClusterIP     string `json:"clusterIp,omitempty"`
	Deleted       bool   `json:"deleted,omitempty"`
	StopRequested bool   `json:"stopRequested,omitempty"`
	Error         string `json:"error,omitempty"`
}
