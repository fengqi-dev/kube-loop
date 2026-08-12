package fileapi

import (
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

type Document struct {
	ID         string           `json:"id"`
	SessionID  string           `json:"sessionId"`
	Namespace  string           `json:"namespace"`
	State      remotetask.State `json:"state"`
	Direction  string           `json:"direction"`
	Kind       string           `json:"kind"`
	Pod        string           `json:"pod"`
	Container  string           `json:"container"`
	RemotePath string           `json:"remotePath"`
	Size       uint64           `json:"size,omitempty"`
	Offset     uint64           `json:"offset,omitempty"`
	Checksum   string           `json:"checksum,omitempty"`
	Overwrite  bool             `json:"overwrite,omitempty"`
	ResumeID   string           `json:"resumeId,omitempty"`
	CreatedAt  time.Time        `json:"createdAt"`
	UpdatedAt  time.Time        `json:"updatedAt"`
	ExpiresAt  time.Time        `json:"expiresAt"`
}
