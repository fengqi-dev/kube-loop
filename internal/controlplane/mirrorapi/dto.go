package mirrorapi

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
	Service      string                     `json:"service"`
	ClusterIP    string                     `json:"clusterIp"`
	Ports        []servicemodel.Port        `json:"ports"`
	LocalTargets []servicemodel.LocalTarget `json:"localTargets,omitempty"`
	CreatedAt    time.Time                  `json:"createdAt"`
	UpdatedAt    time.Time                  `json:"updatedAt"`
	ExpiresAt    time.Time                  `json:"expiresAt"`
}

type listDocument struct {
	Items []Document `json:"items"`
}
