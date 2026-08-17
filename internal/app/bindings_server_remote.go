package app

import (
	"errors"
	"slices"
	"strings"

	clientdataplane "github.com/fengqi-dev/kube-loop/internal/client/dataplane"
	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/networkdiag"
)

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
		return RemoteInventory{}, errors.New("Remote Cluster Backend is unavailable")
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
		if a.remoteFiles != nil {
			if err := a.remoteFiles.StopProfile(serverProfile.ID); err != nil {
				return RemoteInventory{}, err
			}
		}
		if a.remoteExecs != nil {
			if err := a.remoteExecs.StopProfile(serverProfile.ID); err != nil {
				return RemoteInventory{}, err
			}
		}
		if a.remoteSSH != nil {
			if err := a.remoteSSH.StopProfile(serverProfile.ID); err != nil {
				return RemoteInventory{}, err
			}
		}
		if a.remoteForwards != nil {
			if err := a.remoteForwards.StopProfile(a.context(), serverProfile.ID); err != nil {
				return RemoteInventory{}, err
			}
		}
		if a.remoteExchanges != nil {
			if err := a.remoteExchanges.StopProfile(a.context(), serverProfile.ID); err != nil {
				return RemoteInventory{}, err
			}
		}
		if a.remoteMirrors != nil {
			if err := a.remoteMirrors.StopProfile(a.context(), serverProfile.ID); err != nil {
				return RemoteInventory{}, err
			}
		}
		if a.remotePreviews != nil {
			if err := a.remotePreviews.StopProfile(a.context(), serverProfile.ID); err != nil {
				return RemoteInventory{}, err
			}
		}
		if a.dataPlanes != nil {
			if err := a.dataPlanes.Disconnect(serverProfile.ID); err != nil {
				return RemoteInventory{}, err
			}
		}
		if err := a.remoteSessions.Disconnect(a.context(), serverProfile.ID); err != nil {
			return RemoteInventory{}, err
		}
		return result, nil
	}
	if current, currentErr := a.remoteSessions.Current(serverProfile.ID); currentErr == nil && current.Namespace != selected {
		if a.remoteFiles != nil {
			if err := a.remoteFiles.StopProfile(serverProfile.ID); err != nil {
				return RemoteInventory{}, err
			}
		}
		if a.remoteExecs != nil {
			if err := a.remoteExecs.StopProfile(serverProfile.ID); err != nil {
				return RemoteInventory{}, err
			}
		}
		if a.remoteSSH != nil {
			if err := a.remoteSSH.StopProfile(serverProfile.ID); err != nil {
				return RemoteInventory{}, err
			}
		}
		if a.remoteForwards != nil {
			if err := a.remoteForwards.StopProfile(a.context(), serverProfile.ID); err != nil {
				return RemoteInventory{}, err
			}
		}
		if a.remoteExchanges != nil {
			if err := a.remoteExchanges.StopProfile(a.context(), serverProfile.ID); err != nil {
				return RemoteInventory{}, err
			}
		}
		if a.remoteMirrors != nil {
			if err := a.remoteMirrors.StopProfile(a.context(), serverProfile.ID); err != nil {
				return RemoteInventory{}, err
			}
		}
		if a.remotePreviews != nil {
			if err := a.remotePreviews.StopProfile(a.context(), serverProfile.ID); err != nil {
				return RemoteInventory{}, err
			}
		}
		if a.dataPlanes != nil {
			if err := a.dataPlanes.Disconnect(serverProfile.ID); err != nil {
				return RemoteInventory{}, err
			}
		}
	}
	session, err := a.remoteSessions.Connect(a.context(), serverProfile, selected)
	if err != nil {
		return RemoteInventory{}, err
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
				dataPlane = clientdataplane.Status{State: "disconnected", Mode: "socks"}
			}
			result.DataPlane = &dataPlane
		} else if err := a.dataPlanes.Disconnect(serverProfile.ID); err != nil {
			return RemoteInventory{}, err
		}
	}
	a.startServerInventoryWatch(serverProfile, selected, result.Capabilities)
	return result, nil
}

func containsRemoteNamespace(namespaces []clientremote.Namespace, selected string) bool {
	for _, namespace := range namespaces {
		if namespace.Name == selected {
			return true
		}
	}
	return false
}
