package fileopsapi

import (
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

type ListDocument struct {
	SessionID string  `json:"sessionId"`
	Namespace string  `json:"namespace"`
	Pod       string  `json:"pod"`
	Container string  `json:"container"`
	Path      string  `json:"path"`
	Items     []Entry `json:"items"`
}

type Document struct {
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
	Result      Result           `json:"result"`
	CreatedAt   time.Time        `json:"createdAt"`
	UpdatedAt   time.Time        `json:"updatedAt"`
	ExpiresAt   time.Time        `json:"expiresAt"`
}
