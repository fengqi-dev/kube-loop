package exchangeapi

import (
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficmodel"
)

type Document struct {
	ID        string              `json:"id"`
	SessionID string              `json:"sessionId"`
	Namespace string              `json:"namespace"`
	State     remotetask.State    `json:"state"`
	Service   string              `json:"service"`
	ClusterIP string              `json:"clusterIp"`
	Ports     []trafficmodel.Port `json:"ports"`
	CreatedAt time.Time           `json:"createdAt"`
	UpdatedAt time.Time           `json:"updatedAt"`
	ExpiresAt time.Time           `json:"expiresAt"`
}

type storedSpec struct {
	Service   string              `json:"service"`
	ClusterIP string              `json:"clusterIp"`
	Ports     []trafficmodel.Port `json:"ports"`
}
