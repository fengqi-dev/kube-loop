package tui

const (
	keyBackspace = "backspace"
	keyCtrlC     = "ctrl+c"
	keyDown      = "down"
	keyEnter     = "enter"
	keyEsc       = "esc"
	keyLeft      = "left"
	keyRight     = "right"
	keyShiftTab  = "shift+tab"
	keyTab       = "tab"
)

const (
	tabNameConnection = "Connection"

	dataPlaneStateConnected    = "connected"
	dataPlaneStateDisconnected = "disconnected"
	statusDataPlaneConnected   = "Data plane connected"
	consoleStateConnected      = "Connected"

	taskKindExec        = "exec"
	taskKindForward     = "forward"
	taskKindPreview     = "preview"
	taskKindSSH         = "ssh"
	taskKindExchange    = "exchange"
	taskKindMirror      = "mirror"
	taskActionPaused    = "paused"
	taskActionResumed   = "resumed"
	taskActionDeleted   = "deleted"
	taskStateRunning    = "running"
	taskStateStopped    = "stopped"
	resourceKindPod     = "pod"
	resourceKindService = "service"
	taskStatusExec      = "EXEC"
	taskStatusSSH       = "SSH"

	commandConnection = "conn"
	commandHelp       = "help"
	commandNamespace  = "namespace"
	commandService    = "svc"

	stageCreateClusterSession = "Create Cluster Session"

	errDisconnectBeforeChangingServer = "disconnect before changing server"
	errDisconnectBeforeLogout         = "disconnect before logging out"
)
