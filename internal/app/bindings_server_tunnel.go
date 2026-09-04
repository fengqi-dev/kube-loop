package app

import (
	"errors"
	"fmt"
	"strings"

	clientdataplane "github.com/fengqi-dev/kube-loop/internal/client/dataplane"
	clientprofile "github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/networkdiag"
)

func (a *App) ConnectServerDataPlane(profileID, mode string) (clientdataplane.Status, error) {
	a.logInfo("connect data plane: profile=" + profileID + " mode=" + mode)
	if a.dataPlanes == nil || a.remoteSessions == nil {
		a.logError("connect data plane: unavailable")
		return clientdataplane.Status{}, errors.New("data plane is unavailable")
	}
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		a.logError("connect data plane: " + err.Error())
		return clientdataplane.Status{}, err
	}
	session, err := a.remoteSessions.Current(serverProfile.ID)
	if err != nil {
		session, err = a.remoteSessions.Connect(a.context(), serverProfile, session.Namespace)
		if err != nil {
			a.logError("connect data plane session: " + err.Error())
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
				a.logError("TUN blocked by network conflict: " + issue.Message)
				return clientdataplane.Status{}, fmt.Errorf(
					"cannot install TUN while a local network conflict exists: %s",
					issue.Message,
				)
			}
		}
	}
	status, err := a.dataPlanes.Connect(a.context(), serverProfile, session)
	if err != nil {
		a.logError("connect data plane: " + err.Error())
		return clientdataplane.Status{}, err
	}
	a.logInfo("data plane connected mode=" + status.Mode)
	if mode == tunnelModeTUN && status.Mode != tunnelModeTUN {
		a.logInfo("starting TUN for profile=" + serverProfile.ID)
		status, err = a.dataPlanes.StartTUN(a.context(), serverProfile.ID)
		if err != nil {
			a.logError("start TUN failed: " + err.Error())
			return clientdataplane.Status{}, errors.Join(err, a.dataPlanes.Disconnect(serverProfile.ID))
		}
		a.logInfo("TUN started mode=" + status.Mode)
	}
	a.restoreConnectedTasks(serverProfile)
	return status, nil
}

// restoreConnectedTasks re-materializes local feature listeners (Port Forward,
// Exchange, Mirror, Preview) once the Data Plane SOCKS endpoint is available.
// Restoration is attempted inside LoadServerInventory, which precedes the
// tunnel connection, so running TrafficBindings can only be brought back up
// locally after the Data Plane connects.
func (a *App) restoreConnectedTasks(serverProfile clientprofile.Profile) {
	if a.remoteSessions == nil {
		return
	}
	session, err := a.remoteSessions.Current(serverProfile.ID)
	if err != nil || session.State != "active" {
		return
	}
	if err := a.restoreServerTasks(serverProfile, session); err != nil {
		a.logWarn("Data Plane task restoration unavailable: " + err.Error())
	}
}

func (a *App) DisconnectServerDataPlane(profileID string) (clientdataplane.Status, error) {
	a.logInfo("disconnect data plane: profile=" + profileID)
	if a.dataPlanes == nil {
		return clientdataplane.Status{}, errors.New("data plane is unavailable")
	}
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		return clientdataplane.Status{}, err
	}
	releaseErr := a.releaseServerTasks(serverProfile.ID)
	if err := a.dataPlanes.Disconnect(serverProfile.ID); err != nil {
		return clientdataplane.Status{}, errors.Join(releaseErr, err)
	}
	if releaseErr != nil {
		a.logWarn("Data Plane local listeners released with errors: " + releaseErr.Error())
	}
	a.logInfo("data plane disconnected profile=" + profileID)
	return clientdataplane.Status{State: remoteStateDisconnected, Mode: tunnelModeSOCKS}, nil
}

// releaseServerTasks drains the active local listeners of a profile without
// touching the gateway tasks, freeing the local host ports they occupy. Tasks
// are only released, not paused on the Gateway: Running TrafficBindings stay
// Running and the next connect Restore re-materializes them, closing the loop
// (connect restores, disconnect releases). Release is idempotent: released or
// paused entries are skipped, so repeated disconnects are safe.
func (a *App) releaseServerTasks(profileID string) error {
	var result error
	if a.remoteForwards != nil {
		result = errors.Join(result, a.remoteForwards.ReleaseProfile(a.context(), profileID))
	}
	if a.remoteExchanges != nil {
		result = errors.Join(result, a.remoteExchanges.ReleaseProfile(a.context(), profileID))
	}
	if a.remoteMirrors != nil {
		result = errors.Join(result, a.remoteMirrors.ReleaseProfile(a.context(), profileID))
	}
	if a.remotePreviews != nil {
		result = errors.Join(result, a.remotePreviews.ReleaseProfile(a.context(), profileID))
	}
	return result
}

func (a *App) StartServerTunnel(profileID string) (clientdataplane.Status, error) {
	a.logInfo("StartServerTunnel profile=" + profileID)
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
	status, err := a.dataPlanes.StartTUN(a.context(), serverProfile.ID)
	if err != nil {
		a.logError("StartServerTunnel failed: " + err.Error())
		return clientdataplane.Status{}, err
	}
	a.logInfo("StartServerTunnel ok mode=" + status.Mode)
	return status, nil
}

func (a *App) StopServerTunnel(profileID string) (clientdataplane.Status, error) {
	a.logInfo("stop TUN: profile=" + profileID)
	if a.dataPlanes == nil {
		return clientdataplane.Status{}, errors.New("data plane is unavailable")
	}
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		return clientdataplane.Status{}, err
	}
	status, err := a.dataPlanes.StopTUN(serverProfile.ID)
	if err != nil {
		a.logError("stop TUN failed: " + err.Error())
		return clientdataplane.Status{}, err
	}
	a.logInfo("TUN stopped profile=" + profileID)
	return status, nil
}
