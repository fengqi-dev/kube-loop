// Package supervisorprotocol defines the bounded local RPC contract used to
// manage the macOS privileged worker.
package supervisorprotocol

const (
	Version       = 1
	SchemaVersion = 1

	MaxRequestBytes  = 64 << 10
	MaxResponseBytes = 64 << 10
	MaxWorkerBytes   = 128 << 20

	OpStatus         = "status"
	OpUpdateWorker   = "update-worker"
	OpRollbackWorker = "rollback-worker"
	OpRestartWorker  = "restart-worker"
)

type UpdateManifest struct {
	SchemaVersion             int    `json:"schemaVersion"`
	RequestID                 string `json:"requestId"`
	Channel                   string `json:"channel"`
	Version                   string `json:"version"`
	WorkerProtocol            int    `json:"workerProtocol"`
	MinimumSupervisorProtocol int    `json:"minimumSupervisorProtocol"`
	Size                      int64  `json:"size"`
	SHA256                    string `json:"sha256"`
	Force                     bool   `json:"force,omitempty"`
}

type Request struct {
	Protocol int             `json:"protocol"`
	Op       string          `json:"op"`
	Token    string          `json:"token"`
	Manifest *UpdateManifest `json:"manifest,omitempty"`
}

type WorkerStatus struct {
	Installed      bool     `json:"installed"`
	Running        bool     `json:"running"`
	CoreReady      bool     `json:"coreReady"`
	Version        string   `json:"version,omitempty"`
	Protocol       int      `json:"protocol,omitempty"`
	SHA256         string   `json:"sha256,omitempty"`
	ActiveSessions []string `json:"activeSessions,omitempty"`
}

type Response struct {
	OK                bool         `json:"ok"`
	Error             string       `json:"error,omitempty"`
	Protocol          int          `json:"protocol"`
	Channel           string       `json:"channel,omitempty"`
	Worker            WorkerStatus `json:"worker"`
	RolledBack        bool         `json:"rolledBack,omitempty"`
	PreviousAvailable bool         `json:"previousAvailable,omitempty"`
}
