package helper

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
