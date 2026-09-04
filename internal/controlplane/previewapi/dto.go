package previewapi

import (
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
	"github.com/fengqi-dev/kube-loop/internal/protocol/servicemodel"
)

type Document struct {
	ID           string                     `json:"id"`
	SessionID    string                     `json:"sessionId"`
	Namespace    string                     `json:"namespace"`
	State        remotetask.State           `json:"state"`
	Name         string                     `json:"name"`
	ClusterIP    string                     `json:"clusterIp,omitempty"`
	Ports        []servicemodel.Port        `json:"ports"`
	LocalTargets []servicemodel.LocalTarget `json:"localTargets,omitempty"`
	CreatedAt    time.Time                  `json:"createdAt"`
	UpdatedAt    time.Time                  `json:"updatedAt"`
}

type listDocument struct {
	Items []Document `json:"items"`
}
