package execapi

import (
	"time"

	"github.com/fengqi-dev/kube-loop/internal/remotetask"
)

type Document struct {
	ID        string           `json:"id"`
	SessionID string           `json:"sessionId"`
	Namespace string           `json:"namespace"`
	State     remotetask.State `json:"state"`
	Pod       string           `json:"pod"`
	Container string           `json:"container,omitempty"`
	TTY       bool             `json:"tty"`
	CreatedAt time.Time        `json:"createdAt"`
	UpdatedAt time.Time        `json:"updatedAt"`
	ExpiresAt time.Time        `json:"expiresAt"`
}
