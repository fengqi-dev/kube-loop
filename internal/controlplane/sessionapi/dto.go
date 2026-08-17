package sessionapi

import (
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/capability"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
)

type Document struct {
	ID              string               `json:"id"`
	Namespace       string               `json:"namespace"`
	State           string               `json:"state"`
	Generation      uint64               `json:"generation"`
	CreatedAt       time.Time            `json:"createdAt"`
	UpdatedAt       time.Time            `json:"updatedAt"`
	LastHeartbeatAt time.Time            `json:"lastHeartbeatAt"`
	ExpiresAt       time.Time            `json:"expiresAt"`
	NetworkSpec     networkspec.Spec     `json:"networkSpec"`
	NetworkSpecHash string               `json:"networkSpecHash"`
	Capabilities    *capability.Snapshot `json:"capabilities,omitempty"`
}
