package helper

import "github.com/fengqi-dev/kube-loop/internal/singbox"

const (
	ProtocolVersion = 5

	OpPing      = "ping"
	OpStart     = "start"
	OpStop      = "stop"
	OpStopAll   = "stop-all"
	OpStatus    = "status"
	OpUpdateDNS = "update-dns"
)

// Request is a single JSON-line RPC request.
type Request struct {
	Op        string               `json:"op"`
	Token     string               `json:"token,omitempty"`
	Session   *singbox.SessionSpec `json:"session,omitempty"`
	SessionID string               `json:"sessionId,omitempty"`
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
}

// AuthFile is persisted under the system state directory.
type AuthFile struct {
	Token       string `json:"token"`
	UID         int    `json:"uid"`
	Version     string `json:"version"`
	HomeDir     string `json:"homeDir,omitempty"`
	OwnerSID    string `json:"ownerSid,omitempty"`
	SingBoxPath string `json:"singBoxPath,omitempty"`
}

// Status describes helper installation from the desktop app's point of view.
type Status struct {
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	CoreReady bool   `json:"coreReady"`
	Version   string `json:"version,omitempty"`
	Protocol  int    `json:"protocol,omitempty"`
	Expected  string `json:"expected"`
	Socket    string `json:"socket"`
	Error     string `json:"error,omitempty"`
}
