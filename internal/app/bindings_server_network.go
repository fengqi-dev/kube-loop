package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	clientprofile "github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/helper"
	helperinstall "github.com/fengqi-dev/kube-loop/internal/helper/install"
	"github.com/fengqi-dev/kube-loop/internal/singbox"
)

type ServerNetworkSettings struct {
	DNSNamespace string                    `json:"dnsNamespace,omitempty"`
	HostAliases  []clientprofile.HostAlias `json:"hostAliases,omitempty"`
}

func (a *App) GetServerNetworkSettings(profileID string) (ServerNetworkSettings, error) {
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		return ServerNetworkSettings{}, err
	}
	return networkSettings(serverProfile), nil
}

func (a *App) SetServerDNSNamespace(profileID, namespace string) (ServerNetworkSettings, error) {
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		return ServerNetworkSettings{}, err
	}
	previous := serverProfile
	serverProfile.DNSNamespace = namespace
	if err := a.profiles.Upsert(serverProfile); err != nil {
		return ServerNetworkSettings{}, err
	}
	stored, err := a.serverProfile(profileID)
	if err != nil {
		return ServerNetworkSettings{}, err
	}
	if a.dataPlanes != nil {
		if _, statusErr := a.dataPlanes.Status(profileID); statusErr == nil {
			if updateErr := a.dataPlanes.UpdateDNSNamespace(a.context(), profileID, stored.DNSNamespace); updateErr != nil {
				_ = a.profiles.Upsert(previous)
				return ServerNetworkSettings{}, updateErr
			}
		}
	}
	return networkSettings(stored), nil
}

func (a *App) SetServerHostAliases(
	profileID string, aliases []clientprofile.HostAlias,
) (ServerNetworkSettings, error) {
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		return ServerNetworkSettings{}, err
	}
	previous := serverProfile
	serverProfile.HostAliases = append([]clientprofile.HostAlias{}, aliases...)
	if err := a.profiles.Upsert(serverProfile); err != nil {
		return ServerNetworkSettings{}, err
	}
	stored, err := a.serverProfile(profileID)
	if err != nil {
		return ServerNetworkSettings{}, err
	}
	if a.dataPlanes != nil {
		if _, statusErr := a.dataPlanes.Status(profileID); statusErr == nil {
			runtimeAliases := make([]singbox.HostAlias, len(stored.HostAliases))
			for index, item := range stored.HostAliases {
				runtimeAliases[index] = singbox.HostAlias{Domain: item.Domain, IP: item.IP}
			}
			if updateErr := a.dataPlanes.UpdateHostAliases(a.context(), profileID, runtimeAliases); updateErr != nil {
				_ = a.profiles.Upsert(previous)
				return ServerNetworkSettings{}, updateErr
			}
		}
	}
	return networkSettings(stored), nil
}

func (a *App) ServerDataPlaneMetrics(profileID string) (singbox.Metrics, error) {
	if a.dataPlanes == nil {
		return singbox.Metrics{}, errors.New("data plane is unavailable")
	}
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		return singbox.Metrics{}, err
	}
	return a.dataPlanes.Metrics(a.context(), serverProfile.ID)
}

func (a *App) ServerDataPlaneLogs(profileID string) ([]string, error) {
	if a.dataPlanes == nil {
		return nil, errors.New("data plane is unavailable")
	}
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		return nil, err
	}
	return a.dataPlanes.Logs(a.context(), serverProfile.ID)
}

func (a *App) TestServerDataPlane(profileID string) error {
	if a.dataPlanes == nil {
		return errors.New("data plane is unavailable")
	}
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		return err
	}
	return a.dataPlanes.TestConnectivity(a.context(), serverProfile.ID)
}

// GetServerSingBoxConfig returns the generated configuration for a server data plane.
func (a *App) GetServerSingBoxConfig(profileID string) (string, error) {
	if a.dataPlanes == nil || a.profiles == nil {
		return "", errors.New("data plane is unavailable")
	}
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		return "", err
	}
	raw, err := a.dataPlanes.ConfigJSON(serverProfile.ID)
	if err != nil {
		return "", err
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		return string(raw), nil
	}
	return pretty.String(), nil
}

func (a *App) HelperStatus() helper.Status {
	return helper.GetStatus(a.context())
}

func (a *App) InstallHelper() error {
	a.appendLog("INFO", "installing privileged helper")
	if err := helperinstall.EnsureCurrentInstall(a.context()); err != nil {
		a.appendLog("ERROR", fmt.Sprintf("install privileged helper: %v", err))
		return err
	}
	return nil
}

func (a *App) UninstallHelper() error {
	a.appendLog("INFO", "uninstalling privileged helper")
	if err := helperinstall.Uninstall(a.context()); err != nil {
		a.appendLog("ERROR", fmt.Sprintf("uninstall privileged helper: %v", err))
		return err
	}
	return nil
}

func networkSettings(serverProfile clientprofile.Profile) ServerNetworkSettings {
	return ServerNetworkSettings{
		DNSNamespace: serverProfile.DNSNamespace,
		HostAliases:  append([]clientprofile.HostAlias{}, serverProfile.HostAliases...),
	}
}
