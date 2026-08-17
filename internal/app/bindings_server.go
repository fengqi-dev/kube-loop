package app

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/client/credentials"
	clientdiscovery "github.com/fengqi-dev/kube-loop/internal/client/discovery"
	clientprofile "github.com/fengqi-dev/kube-loop/internal/client/profile"
)

type SaveServerProfileRequest struct {
	ID          string `json:"id,omitempty"`
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
		return clientdiscovery.Document{}, errors.New("service discovery is unavailable")
	}
	return a.discovery.Discover(a.context(), serviceAddress)
}

func (a *App) SaveServerProfile(request SaveServerProfileRequest) (ServerProfileResult, error) {
	if a.profiles == nil {
		return ServerProfileResult{}, errors.New("Server Profile store is unavailable")
	}
	document, err := a.TestServerAddress(request.BaseURL)
	if err != nil {
		return ServerProfileResult{}, err
	}
	baseURL, err := requestedServerBaseURL(request.BaseURL, document.PublicURL)
	if err != nil {
		return ServerProfileResult{}, err
	}
	state := a.profiles.Snapshot()
	displayName := strings.TrimSpace(request.DisplayName)
	var previous clientprofile.Profile
	requestedID := strings.TrimSpace(request.ID)
	if requestedID != "" {
		for _, existing := range state.Profiles {
			if existing.ID == requestedID {
				previous = existing
				break
			}
		}
		if previous.ID == "" {
			return ServerProfileResult{}, errors.New("Server Profile not found")
		}
		if document.ServiceID != previous.ID {
			return ServerProfileResult{}, errors.New("the edited address belongs to a different Server")
		}
	} else {
		for _, existing := range state.Profiles {
			if existing.ID != document.ServiceID {
				continue
			}
			if existing.BaseURL != baseURL {
				return ServerProfileResult{}, errors.New("the service ID is already registered with a different address")
			}
			previous = existing
			break
		}
	}
	if displayName == "" && previous.ID != "" {
		displayName = previous.DisplayName
	}
	if displayName == "" {
		displayName = document.ServiceID
	}
	serverProfile := clientprofile.Profile{
		ID: document.ServiceID, BaseURL: baseURL, TunnelPath: document.TunnelPath, DisplayName: displayName,
		LastIdentityID: previous.LastIdentityID, LastUserName: previous.LastUserName,
		LastNamespace: previous.LastNamespace, DNSNamespace: previous.DNSNamespace,
		SOCKSPort:   previous.SOCKSPort,
		HostAliases: append([]clientprofile.HostAlias{}, previous.HostAliases...),
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

func requestedServerBaseURL(requestedValue, advertisedValue string) (string, error) {
	requested, err := clientprofile.NormalizeBaseURL(requestedValue)
	if err != nil {
		return "", err
	}
	requestedURL, err := url.Parse(requested)
	if err != nil {
		return "", errors.New("service address is invalid")
	}
	advertisedURL, err := url.Parse(strings.TrimSpace(advertisedValue))
	if err != nil || advertisedURL.Host == "" {
		return "", errors.New("service discovery public URL is invalid")
	}
	if !strings.EqualFold(requestedURL.Host, advertisedURL.Host) {
		return "", errors.New("service discovery public URL host does not match the requested address")
	}
	return requested, nil
}

func (a *App) SelectServerProfile(id string) (clientprofile.State, error) {
	if a.profiles == nil {
		return clientprofile.State{}, errors.New("Server Profile store is unavailable")
	}
	if err := a.profiles.SetActive(id); err != nil {
		return clientprofile.State{}, err
	}
	return a.profiles.Snapshot(), nil
}

func (a *App) DeleteServerProfile(id string) (clientprofile.State, error) {
	if a.profiles == nil {
		return clientprofile.State{}, errors.New("Server Profile store is unavailable")
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
				cleanupErr = errors.Join(cleanupErr, errors.New("authentication is unavailable"))
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
