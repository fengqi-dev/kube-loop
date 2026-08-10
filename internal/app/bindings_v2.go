package app

import (
	"errors"
	"fmt"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/clientv2/credentials"
	clientdiscovery "github.com/fengqi-dev/kube-loop/internal/clientv2/discovery"
	clientprofile "github.com/fengqi-dev/kube-loop/internal/clientv2/profile"
)

type SaveServerProfileRequest struct {
	BaseURL     string `json:"baseUrl"`
	DisplayName string `json:"displayName,omitempty"`
	Activate    bool   `json:"activate"`
}

type ServerProfileResult struct {
	Profile   clientprofile.Profile    `json:"profile"`
	Discovery clientdiscovery.Document `json:"discovery"`
}

func (a *App) ServerProfiles() clientprofile.State {
	return a.serverProfiles()
}

func (a *App) TestServerAddress(serviceAddress string) (clientdiscovery.Document, error) {
	if a.discovery == nil {
		return clientdiscovery.Document{}, errors.New("V2 service discovery is unavailable")
	}
	return a.discovery.Discover(a.context(), serviceAddress)
}

func (a *App) SaveServerProfile(request SaveServerProfileRequest) (ServerProfileResult, error) {
	if a.profiles == nil {
		return ServerProfileResult{}, errors.New("V2 Server Profile store is unavailable")
	}
	document, err := a.TestServerAddress(request.BaseURL)
	if err != nil {
		return ServerProfileResult{}, err
	}
	state := a.profiles.Snapshot()
	displayName := strings.TrimSpace(request.DisplayName)
	var previous clientprofile.Profile
	for _, existing := range state.Profiles {
		if existing.ID != document.ServiceID {
			continue
		}
		if existing.BaseURL != document.PublicURL {
			return ServerProfileResult{}, errors.New("the service ID is already registered with a different address")
		}
		if displayName == "" {
			displayName = existing.DisplayName
		}
		previous = existing
		break
	}
	if displayName == "" {
		displayName = document.ServiceID
	}
	serverProfile := clientprofile.Profile{
		SchemaVersion: clientprofile.ProfileSchemaVersion,
		ID:            document.ServiceID, BaseURL: document.PublicURL, TunnelPath: document.TunnelPath, DisplayName: displayName,
		LastPrincipalID: previous.LastPrincipalID, LastUserName: previous.LastUserName,
		LastNamespace: previous.LastNamespace,
	}
	if err := a.profiles.Upsert(serverProfile); err != nil {
		return ServerProfileResult{}, err
	}
	if request.Activate {
		if err := a.profiles.SetActive(serverProfile.ID); err != nil {
			return ServerProfileResult{}, err
		}
	}
	return ServerProfileResult{Profile: serverProfile, Discovery: document}, nil
}

func (a *App) SelectServerProfile(id string) (clientprofile.State, error) {
	if a.profiles == nil {
		return clientprofile.State{}, errors.New("V2 Server Profile store is unavailable")
	}
	if err := a.profiles.SetActive(id); err != nil {
		return clientprofile.State{}, err
	}
	return a.profiles.Snapshot(), nil
}

func (a *App) DeleteServerProfile(id string) (clientprofile.State, error) {
	if a.profiles == nil {
		return clientprofile.State{}, errors.New("V2 Server Profile store is unavailable")
	}
	serverProfile, err := a.serverProfile(id)
	if err != nil {
		return clientprofile.State{}, err
	}
	a.stopServerInventoryWatch(serverProfile.ID)
	var cleanupErr error
	if a.remoteFiles != nil {
		if err := a.remoteFiles.StopProfile(serverProfile.ID); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("stop Server Profile file transfers before deletion: %w", err))
		}
	}
	if a.remoteExecs != nil {
		if err := a.remoteExecs.StopProfile(serverProfile.ID); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("stop Server Profile Pod exec streams before deletion: %w", err))
		}
	}
	if a.remoteSSH != nil {
		if err := a.remoteSSH.StopProfile(serverProfile.ID); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("stop Server Profile Pod SSH endpoints before deletion: %w", err))
		}
	}
	if a.remoteForwards != nil {
		if err := a.remoteForwards.StopProfile(a.context(), serverProfile.ID); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("stop Server Profile Port Forwards before deletion: %w", err))
		}
	}
	if a.remoteExchanges != nil {
		if err := a.remoteExchanges.StopProfile(a.context(), serverProfile.ID); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("stop Server Profile Exchanges before deletion: %w", err))
		}
	}
	if a.remoteMirrors != nil {
		if err := a.remoteMirrors.StopProfile(a.context(), serverProfile.ID); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("stop Server Profile Mirrors before deletion: %w", err))
		}
	}
	if a.remotePreviews != nil {
		if err := a.remotePreviews.StopProfile(a.context(), serverProfile.ID); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("stop Server Profile Previews before deletion: %w", err))
		}
	}
	if a.dataPlanes != nil {
		if err := a.dataPlanes.Disconnect(serverProfile.ID); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("disconnect Server Profile Data Plane before deletion: %w", err))
		}
	}
	if a.remoteSessions != nil {
		if err := a.remoteSessions.Disconnect(a.context(), serverProfile.ID); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("disconnect Server Profile session before deletion: %w", err))
		}
	}
	if a.credentials != nil {
		credential, credentialErr := a.credentials.Get(serverProfile.ID)
		switch {
		case credentialErr == nil:
			if a.auth == nil {
				cleanupErr = errors.Join(cleanupErr, errors.New("V2 authentication is unavailable"))
			} else if err := a.auth.Revoke(a.context(), serverProfile.BaseURL, credential.RefreshToken); err != nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("revoke Server Profile login before deletion: %w", err))
			}
			if err := a.credentials.Delete(serverProfile.ID); err != nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("delete Server Profile credentials: %w", err))
			}
		case !errors.Is(credentialErr, credentials.ErrNotFound):
			cleanupErr = errors.Join(cleanupErr, credentialErr)
		}
	}
	if err := a.profiles.Remove(id); err != nil {
		cleanupErr = errors.Join(cleanupErr, err)
	}
	return a.profiles.Snapshot(), cleanupErr
}

func (a *App) serverProfiles() clientprofile.State {
	if a.profiles == nil {
		return clientprofile.State{Version: 1, Profiles: []clientprofile.Profile{}}
	}
	return a.profiles.Snapshot()
}
