// Package helperprotocol defines the versioned JSON-line contract shared by
// the desktop process and the privileged local helper.
package helperprotocol

import "github.com/fengqi-dev/kube-loop/internal/singbox"

const (
	Version = 6

	OpPing      = "ping"
	OpStart     = "start"
	OpStop      = "stop"
	OpStopAll   = "stop-all"
	OpStatus    = "status"
	OpUpdateDNS = "update-dns"
	OpReadLogs  = "read-logs"
)

// Request is a single JSON-line RPC request.
type Request struct {
	Op        string               `json:"op"`
	Token     string               `json:"token,omitempty"`
	Session   *singbox.SessionSpec `json:"session,omitempty"`
	SessionID string               `json:"sessionId,omitempty"`
	LogOffset int64                `json:"logOffset,omitempty"`
	DNS       *singbox.DNSMeta     `json:"dns,omitempty"`
}

// Response is a single JSON-line RPC response.
type Response struct {
	OK             bool     `json:"ok"`
	Error          string   `json:"error,omitempty"`
	Version        string   `json:"version,omitempty"`
	Protocol       int      `json:"protocol,omitempty"`
	Installed      bool     `json:"installed,omitempty"`
	Running        bool     `json:"running,omitempty"`
	CoreReady      bool     `json:"coreReady,omitempty"`
	PID            int      `json:"pid,omitempty"`
	ActiveSessions []string `json:"activeSessions,omitempty"`
	LogData        string   `json:"logData,omitempty"`
	LogOffset      int64    `json:"logOffset,omitempty"`
}
