package app

import (
	"errors"
	"fmt"

	clientdataplane "github.com/fengqi-dev/kube-loop/internal/clientv2/dataplane"
	"github.com/fengqi-dev/kube-loop/internal/networkdiag"
)

func (a *App) StartServerTunnel(profileID string) (clientdataplane.Status, error) {
	if a.dataPlanes == nil || a.remoteSessions == nil {
		return clientdataplane.Status{}, errors.New("V2 Data Plane is unavailable")
	}
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		return clientdataplane.Status{}, err
	}
	session, err := a.remoteSessions.Current(serverProfile.ID)
	if err != nil {
		return clientdataplane.Status{}, err
	}
	diagnostics := networkdiag.InspectNetworkSpec(session.NetworkSpec)
	for _, issue := range diagnostics.Issues {
		if issue.Severity == networkdiag.SeverityWarning {
			return clientdataplane.Status{}, fmt.Errorf("cannot install V2 TUN while a local network conflict exists: %s", issue.Message)
		}
	}
	return a.dataPlanes.StartTUN(a.context(), serverProfile.ID)
}

func (a *App) StopServerTunnel(profileID string) (clientdataplane.Status, error) {
	if a.dataPlanes == nil {
		return clientdataplane.Status{}, errors.New("V2 Data Plane is unavailable")
	}
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		return clientdataplane.Status{}, err
	}
	return a.dataPlanes.StopTUN(serverProfile.ID)
}
