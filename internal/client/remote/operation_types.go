package remote

import (
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

type ExecSpec struct {
	Pod       string   `json:"pod"`
	Container string   `json:"container,omitempty"`
	Command   []string `json:"command"`
	TTY       bool     `json:"tty"`
}

type ExecTask struct {
	ID        string           `json:"id"`
	SessionID string           `json:"sessionId"`
	Namespace string           `json:"namespace"`
	State     remotetask.State `json:"state"`
	Pod       string           `json:"pod"`
	Container string           `json:"container,omitempty"`
	TTY       bool             `json:"tty"`
	CreatedAt time.Time        `json:"createdAt"           ts_type:"string"`
	UpdatedAt time.Time        `json:"updatedAt"           ts_type:"string"`
	ExpiresAt time.Time        `json:"expiresAt"           ts_type:"string"`
}

type FileTransferSpec struct {
	Direction  string `json:"direction"`
	Kind       string `json:"kind"`
	Pod        string `json:"pod"`
	Container  string `json:"container,omitempty"`
	RemotePath string `json:"remotePath"`
	Size       uint64 `json:"size,omitempty"`
	Offset     uint64 `json:"offset,omitempty"`
	Checksum   string `json:"checksum,omitempty"`
	Overwrite  bool   `json:"overwrite,omitempty"`
	ResumeID   string `json:"resumeId,omitempty"`
}

type FileTransferTask struct {
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
	CreatedAt  time.Time        `json:"createdAt"           ts_type:"string"`
	UpdatedAt  time.Time        `json:"updatedAt"           ts_type:"string"`
	ExpiresAt  time.Time        `json:"expiresAt"           ts_type:"string"`
}

type PodFileSpec struct {
	Pod         string `json:"pod"`
	Container   string `json:"container,omitempty"`
	Path        string `json:"path"`
	Destination string `json:"destination,omitempty"`
	Kind        string `json:"kind,omitempty"`
	Recursive   bool   `json:"recursive,omitempty"`
}

type PodFileEntry struct {
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	Kind       string    `json:"kind"`
	Size       int64     `json:"size"`
	Mode       string    `json:"mode"`
	ModifiedAt time.Time `json:"modifiedAt" ts_type:"string"`
}

type PodFileList struct {
	SessionID string         `json:"sessionId"`
	Namespace string         `json:"namespace"`
	Pod       string         `json:"pod"`
	Container string         `json:"container"`
	Path      string         `json:"path"`
	Items     []PodFileEntry `json:"items"`
}

type PodFileResult struct {
	Completed bool   `json:"completed"`
	Error     string `json:"error,omitempty"`
}

type PodFileTask struct {
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
	Result      PodFileResult    `json:"result"`
	CreatedAt   time.Time        `json:"createdAt"             ts_type:"string"`
	UpdatedAt   time.Time        `json:"updatedAt"             ts_type:"string"`
	ExpiresAt   time.Time        `json:"expiresAt"             ts_type:"string"`
}
