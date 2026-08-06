package helper

import (
	"context"
	"os"
)

// GetStatus probes whether the helper is installed and reachable.
func GetStatus(ctx context.Context) Status {
	status := Status{
		Expected: Version,
		Socket:   SocketPath(),
	}
	if _, err := os.Stat(BinaryInstallPath()); err == nil {
		status.Installed = true
	}
	token, err := ReadUserToken()
	if err != nil {
		status.Error = err.Error()
		return status
	}
	client := &Client{Token: token, Dial: dialHelper}
	pingCtx, cancel := withDialTimeout(ctx)
	defer cancel()
	response, err := client.Ping(pingCtx)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	// The running service is authoritative. On Windows, a dev/test client may
	// execute outside the packaged install root, so its local BinaryInstallPath
	// is not necessarily the service binary path.
	status.Installed = status.Installed || response.Installed
	status.Running = true
	status.CoreReady = response.CoreReady
	status.Version = response.Version
	status.Protocol = response.Protocol
	return status
}
