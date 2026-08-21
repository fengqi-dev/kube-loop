//go:build darwin

package supervisor

import (
	"path/filepath"

	"github.com/fengqi-dev/kube-loop/internal/helper"
)

const (
	developmentVersion = "dev"
	releaseChannel     = "release"
)

type Config struct {
	Channel          string
	ServiceLabel     string
	WorkerLabel      string
	BinaryPath       string
	SocketPath       string
	StateDir         string
	LogPath          string
	WorkerBinaryPath string
	WorkerPlistPath  string
}

func CurrentConfig() Config {
	dev := Version == "" || Version == developmentVersion
	channel := releaseChannel
	label := "dev.fengqi.kubeloop.supervisor"
	stateDir := "/var/lib/kubeloop/supervisor"
	socket := "/var/run/kubeloop/supervisor.sock"
	logPath := "/var/log/kubeloop-supervisor.log"
	if dev {
		channel = developmentVersion
		label = "dev.fengqi.kubeloop.supervisor.dev"
		stateDir = "/var/lib/kubeloop-dev/supervisor"
		socket = "/var/run/kubeloop-dev/supervisor.sock"
		logPath = "/var/log/kubeloop-supervisor-dev.log"
	}
	return Config{
		Channel: channel, ServiceLabel: label, WorkerLabel: helper.ServiceLabel(),
		BinaryPath: "/Library/PrivilegedHelperTools/" + label,
		SocketPath: socket, StateDir: stateDir, LogPath: logPath,
		WorkerBinaryPath: helper.BinaryInstallPath(),
		WorkerPlistPath:  "/Library/LaunchDaemons/" + helper.ServiceLabel() + ".plist",
	}
}

func (c Config) AuthPath() string     { return filepath.Join(c.StateDir, "auth.json") }
func (c Config) JournalPath() string  { return filepath.Join(c.StateDir, "update-journal.json") }
func (c Config) PreviousPath() string { return filepath.Join(c.StateDir, "worker.previous") }
func (c Config) LockPath() string     { return filepath.Join(c.StateDir, "update.lock") }
func (c Config) PlistPath() string    { return "/Library/LaunchDaemons/" + c.ServiceLabel + ".plist" }
