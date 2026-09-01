package app

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	clientdataplane "github.com/fengqi-dev/kube-loop/internal/client/dataplane"
	clientprofile "github.com/fengqi-dev/kube-loop/internal/client/profile"
	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/networkdiag"
)

const serverTaskSynchronizationTimeout = 3 * time.Second

type RemoteInventory struct {
	KubernetesVersion string                   `json:"kubernetesVersion"`
	GatewayVersion    string                   `json:"gatewayVersion"`
	Namespaces        []clientremote.Namespace `json:"namespaces"`
	Namespace         string                   `json:"namespace,omitempty"`
	Capabilities      []string                 `json:"capabilities"`
	Pods              []clientremote.Pod       `json:"pods"`
	Services          []clientremote.Service   `json:"services"`
	Session           *clientremote.Session    `json:"session,omitempty"`
	Network           *networkdiag.Result      `json:"network,omitempty"`
	DataPlane         *clientdataplane.Status  `json:"dataPlane,omitempty"`
}

func (a *App) LoadServerInventory(profileID, namespace string) (RemoteInventory, error) {
	if a.remote == nil || a.remoteSessions == nil {
		return RemoteInventory{}, errors.New("remote cluster backend is unavailable")
	}
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		return RemoteInventory{}, err
	}
	version, err := a.remote.Version(a.context(), serverProfile)
	if err != nil {
		return RemoteInventory{}, err
	}
	namespaces, err := a.remote.Namespaces(a.context(), serverProfile)
	if err != nil {
		return RemoteInventory{}, err
	}
	slices.SortFunc(namespaces, func(left, right clientremote.Namespace) int {
		return strings.Compare(left.Name, right.Name)
	})
	selected := strings.TrimSpace(namespace)
	if selected == "" {
		selected = strings.TrimSpace(serverProfile.LastNamespace)
	}
	if !containsRemoteNamespace(namespaces, selected) {
		selected = ""
		if len(namespaces) > 0 {
			selected = namespaces[0].Name
		}
	}
	a.stopServerInventoryWatch(serverProfile.ID)
	result := RemoteInventory{
		KubernetesVersion: version.GitVersion, Namespaces: namespaces, Namespace: selected,
		Capabilities: []string{}, Pods: []clientremote.Pod{}, Services: []clientremote.Service{},
	}
	if selected == "" {
		if err := a.stopServerRuntime(serverProfile.ID, true); err != nil {
			return RemoteInventory{}, err
		}
		return result, nil
	}
	current, currentErr := a.remoteSessions.Current(serverProfile.ID)
	if currentErr == nil && current.Namespace != selected {
		if err := a.stopServerRuntime(serverProfile.ID, false); err != nil {
			return RemoteInventory{}, err
		}
	}
	session, err := a.remoteSessions.Connect(a.context(), serverProfile, selected)
	if err != nil {
		return RemoteInventory{}, err
	}
	synchronized, synchronizationErr := a.synchronizeTrafficBindings(serverProfile, session)
	if synchronizationErr != nil {
		a.appendLog("WARN", "Traffic Binding synchronization unavailable: "+synchronizationErr.Error())
	} else {
		session = synchronized
		if restoreErr := a.restoreServerTasks(serverProfile, session); restoreErr != nil {
			a.appendLog("WARN", "Server task restoration unavailable: "+restoreErr.Error())
		}
	}
	result.Session = &session
	network := networkdiag.InspectNetworkSpec(session.NetworkSpec)
	result.Network = &network
	capabilities, err := a.remote.Capabilities(a.context(), serverProfile, selected)
	if err != nil {
		return RemoteInventory{}, err
	}
	result.GatewayVersion = capabilities.GatewayVersion
	result.Capabilities = append([]string(nil), capabilities.Capabilities...)
	if slices.Contains(result.Capabilities, "pods.list") {
		result.Pods, err = a.remote.Pods(a.context(), serverProfile, selected)
		if err != nil {
			return RemoteInventory{}, err
		}
		slices.SortFunc(result.Pods, func(left, right clientremote.Pod) int {
			return strings.Compare(left.Name, right.Name)
		})
	}
	if slices.Contains(result.Capabilities, "services.list") {
		result.Services, err = a.remote.Services(a.context(), serverProfile, selected)
		if err != nil {
			return RemoteInventory{}, err
		}
		slices.SortFunc(result.Services, func(left, right clientremote.Service) int {
			return strings.Compare(left.Name, right.Name)
		})
	}
	if serverProfile.LastNamespace != selected {
		serverProfile.LastNamespace = selected
		if err := a.profiles.Upsert(serverProfile); err != nil {
			return RemoteInventory{}, err
		}
	}
	if a.dataPlanes != nil {
		if slices.Contains(result.Capabilities, "cluster.tunnel") {
			dataPlane, statusErr := a.dataPlanes.Status(serverProfile.ID)
			if statusErr != nil {
				dataPlane = clientdataplane.Status{State: remoteStateDisconnected, Mode: tunnelModeSOCKS}
			}
			result.DataPlane = &dataPlane
		} else if err := a.dataPlanes.Disconnect(serverProfile.ID); err != nil {
			return RemoteInventory{}, err
		}
	}
	a.startServerInventoryWatch(serverProfile, selected, result.Capabilities)
	return result, nil
}

// DeleteServerTrafficBinding deletes a user-owned CRD that has no local
// runtime entry. Active local tasks use their mode-specific manager instead so
// listeners and reverse-relay resources are closed before the CRD is removed.
func (a *App) DeleteServerTrafficBinding(profileID, taskID string) error {
	if a.remote == nil || a.remoteSessions == nil {
		return errors.New("remote cluster backend is unavailable")
	}
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		return err
	}
	session, err := a.remoteSessions.Current(serverProfile.ID)
	if err != nil {
		return err
	}
	return a.remote.DeleteTrafficBinding(
		a.context(), serverProfile, session, taskID,
	)
}

func (a *App) synchronizeTrafficBindings(
	serverProfile clientprofile.Profile,
	session clientremote.Session,
) (clientremote.Session, error) {
	ctx, cancel := context.WithTimeout(a.context(), serverTaskSynchronizationTimeout)
	defer cancel()
	synchronized, err := a.remote.SyncTrafficBindings(ctx, serverProfile, session)
	if err == nil {
		return synchronized, nil
	}
	var apiError *clientremote.APIError
	if errors.As(err, &apiError) && apiError.Status == 404 {
		return session, nil
	}
	return clientremote.Session{}, err
}

func (a *App) restoreServerTasks(
	serverProfile clientprofile.Profile,
	session clientremote.Session,
) error {
	var result error
	if a.remoteForwards != nil {
		result = errors.Join(result, optionalTaskRestore(
			a.remoteForwards.Restore(a.context(), serverProfile, session),
		))
	}
	if a.remoteExchanges != nil {
		result = errors.Join(result, optionalTaskRestore(
			a.remoteExchanges.Restore(a.context(), serverProfile, session),
		))
	}
	if a.remoteMirrors != nil {
		result = errors.Join(result, optionalTaskRestore(
			a.remoteMirrors.Restore(a.context(), serverProfile, session),
		))
	}
	if a.remotePreviews != nil {
		result = errors.Join(result, optionalTaskRestore(
			a.remotePreviews.Restore(a.context(), serverProfile, session),
		))
	}
	return result
}

func optionalTaskRestore(err error) error {
	var apiError *clientremote.APIError
	if errors.As(err, &apiError) && apiError.Status == 404 {
		return nil
	}
	return err
}

func (a *App) stopServerRuntime(profileID string, disconnectSession bool) error {
	if a.remoteFiles != nil {
		if err := a.remoteFiles.StopProfile(profileID); err != nil {
			return err
		}
	}
	if a.remoteExecs != nil {
		if err := a.remoteExecs.StopProfile(profileID); err != nil {
			return err
		}
	}
	if a.remoteSSH != nil {
		if err := a.remoteSSH.StopProfile(profileID); err != nil {
			return err
		}
	}
	if a.remoteForwards != nil {
		if err := a.remoteForwards.PauseProfile(a.context(), profileID); err != nil {
			return err
		}
	}
	if a.remoteExchanges != nil {
		if err := a.remoteExchanges.PauseProfile(a.context(), profileID); err != nil {
			return err
		}
	}
	if a.remoteMirrors != nil {
		if err := a.remoteMirrors.PauseProfile(a.context(), profileID); err != nil {
			return err
		}
	}
	if a.remotePreviews != nil {
		if err := a.remotePreviews.PauseProfile(a.context(), profileID); err != nil {
			return err
		}
	}
	if a.dataPlanes != nil {
		if err := a.dataPlanes.Disconnect(profileID); err != nil {
			return err
		}
	}
	if disconnectSession {
		return a.remoteSessions.Disconnect(a.context(), profileID)
	}
	return nil
}

func containsRemoteNamespace(namespaces []clientremote.Namespace, selected string) bool {
	for _, namespace := range namespaces {
		if namespace.Name == selected {
			return true
		}
	}
	return false
}
