package app

import (
	"errors"
	"fmt"
	"strings"

	clientdataplane "github.com/fengqi-dev/kube-loop/internal/client/dataplane"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkdiag"
)

func (a *App) ConnectServerDataPlane(profileID, mode string) (clientdataplane.Status, error) {
	if a.dataPlanes == nil || a.remoteSessions == nil {
		return clientdataplane.Status{}, errors.New("data plane is unavailable")
	}
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		return clientdataplane.Status{}, err
	}
	session, err := a.remoteSessions.Current(serverProfile.ID)
	if err != nil {
		session, err = a.remoteSessions.Connect(a.context(), serverProfile, session.Namespace)
		if err != nil {
			return clientdataplane.Status{}, err
		}
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != tunnelModeSOCKS && mode != tunnelModeTUN {
		return clientdataplane.Status{}, errors.New("data plane mode must be socks or tun")
	}
	if mode == tunnelModeTUN {
		diagnostics := networkdiag.InspectNetworkSpec(session.NetworkSpec)
		for _, issue := range diagnostics.Issues {
			if issue.Severity == networkdiag.SeverityWarning {
				return clientdataplane.Status{}, fmt.Errorf(
					"cannot install TUN while a local network conflict exists: %s",
					issue.Message,
				)
			}
		}
	}
	status, err := a.dataPlanes.Connect(a.context(), serverProfile, session)
	if err != nil {
		return clientdataplane.Status{}, err
	}
	if mode == tunnelModeTUN && status.Mode != tunnelModeTUN {
		status, err = a.dataPlanes.StartTUN(a.context(), serverProfile.ID)
		if err != nil {
			return clientdataplane.Status{}, errors.Join(err, a.dataPlanes.Disconnect(serverProfile.ID))
		}
	}
	return status, nil
}

func (a *App) DisconnectServerDataPlane(profileID string) (clientdataplane.Status, error) {
	if a.dataPlanes == nil {
		return clientdataplane.Status{}, errors.New("data plane is unavailable")
	}
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		return clientdataplane.Status{}, err
	}
	if err := a.dataPlanes.Disconnect(serverProfile.ID); err != nil {
		return clientdataplane.Status{}, err
	}
	return clientdataplane.Status{State: remoteStateDisconnected, Mode: tunnelModeSOCKS}, nil
}

func (a *App) StartServerTunnel(profileID string) (clientdataplane.Status, error) {
	if a.dataPlanes == nil || a.remoteSessions == nil {
		return clientdataplane.Status{}, errors.New("data plane is unavailable")
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
			return clientdataplane.Status{}, fmt.Errorf(
				"cannot install TUN while a local network conflict exists: %s",
				issue.Message,
			)
		}
	}
	return a.dataPlanes.StartTUN(a.context(), serverProfile.ID)
}

func (a *App) StopServerTunnel(profileID string) (clientdataplane.Status, error) {
	if a.dataPlanes == nil {
		return clientdataplane.Status{}, errors.New("data plane is unavailable")
	}
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		return clientdataplane.Status{}, err
	}
	return a.dataPlanes.StopTUN(serverProfile.ID)
}
