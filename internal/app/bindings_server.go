package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/credentials"
	clientdiscovery "github.com/fengqi-dev/kube-loop/internal/client/discovery"
	clientprofile "github.com/fengqi-dev/kube-loop/internal/client/profile"
)

const serverProfileCleanupTimeout = 10 * time.Second

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
		return ServerProfileResult{}, errors.New("server Profile store is unavailable")
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
			return ServerProfileResult{}, errors.New("server Profile not found")
		}
		if document.ServiceID != previous.ID {
			return ServerProfileResult{}, errors.New("the edited address belongs to a different server")
		}
	} else {
		for _, existing := range state.Profiles {
			if existing.ID != document.ServiceID {
				continue
			}
			if existing.BaseURL != baseURL {
				return ServerProfileResult{}, errors.New(
					"the service ID is already registered with a different address",
				)
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
		return clientprofile.State{}, errors.New("server Profile store is unavailable")
	}
	if err := a.profiles.SetActive(id); err != nil {
		return clientprofile.State{}, err
	}
	return a.profiles.Snapshot(), nil
}

func (a *App) DeleteServerProfile(id string) (clientprofile.State, error) {
	if a.profiles == nil {
		return clientprofile.State{}, errors.New("server Profile store is unavailable")
	}
	serverProfile, err := a.serverProfile(id)
	if err != nil {
		return clientprofile.State{}, err
	}
	a.stopServerInventoryWatch(serverProfile.ID)

	// Capture the login credential before removing it so a background revoke can
	// still reach the Gateway with the original refresh token.
	var refreshToken string
	if a.credentials != nil {
		if credential, credentialErr := a.credentials.Get(serverProfile.ID); credentialErr == nil {
			refreshToken = credential.RefreshToken
		}
	}

	// Remove the server Profile and its login state locally and immediately, so
	// deletion never depends on (or is blocked by) Gateway reachability.
	var removeErr error
	if a.credentials != nil {
		if err := a.credentials.Delete(serverProfile.ID); err != nil && !errors.Is(err, credentials.ErrNotFound) {
			removeErr = errors.Join(removeErr, fmt.Errorf("delete Server Profile credentials: %w", err))
		}
	}
	if err := a.profiles.Remove(id); err != nil {
		removeErr = errors.Join(removeErr, err)
	}

	// Remote cleanup (pause traffic, disconnect tunnels, revoke the login) is
	// best-effort and runs in the background with a bounded timeout; if the
	// server is unreachable the local deletion has already succeeded.
	a.cleanupServerProfileRemote(serverProfile, refreshToken)
	return a.profiles.Snapshot(), removeErr
}

func (a *App) cleanupServerProfileRemote(serverProfile clientprofile.Profile, refreshToken string) {
	go func() {
		profileID := serverProfile.ID
		ctx, cancel := context.WithTimeout(context.WithoutCancel(a.context()), serverProfileCleanupTimeout)
		defer cancel()
		var cleanupErr error
		if a.remoteFiles != nil {
			if err := a.remoteFiles.StopProfile(profileID); err != nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("stop Server Profile file transfers: %w", err))
			}
		}
		if a.remoteExecs != nil {
			if err := a.remoteExecs.StopProfile(profileID); err != nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("stop Server Profile Pod exec streams: %w", err))
			}
		}
		if a.remoteSSH != nil {
			if err := a.remoteSSH.StopProfile(profileID); err != nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("stop Server Profile Pod SSH endpoints: %w", err))
			}
		}
		if a.remoteForwards != nil {
			if err := a.remoteForwards.PauseProfile(ctx, profileID); err != nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("pause Server Profile Port Forwards: %w", err))
			}
		}
		if a.remoteExchanges != nil {
			if err := a.remoteExchanges.PauseProfile(ctx, profileID); err != nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("pause Server Profile Exchanges: %w", err))
			}
		}
		if a.remoteMirrors != nil {
			if err := a.remoteMirrors.PauseProfile(ctx, profileID); err != nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("pause Server Profile Mirrors: %w", err))
			}
		}
		if a.remotePreviews != nil {
			if err := a.remotePreviews.PauseProfile(ctx, profileID); err != nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("pause Server Profile Previews: %w", err))
			}
		}
		if a.dataPlanes != nil {
			if err := a.dataPlanes.Disconnect(profileID); err != nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("disconnect Server Profile Data Plane: %w", err))
			}
		}
		if a.remoteSessions != nil {
			if err := a.remoteSessions.Disconnect(ctx, profileID); err != nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("disconnect Server Profile session: %w", err))
			}
		}
		if a.auth != nil && refreshToken != "" {
			if err := a.auth.Revoke(ctx, serverProfile.BaseURL, refreshToken); err != nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("revoke Server Profile login: %w", err))
			}
		}
		if cleanupErr != nil {
			a.logWarn(fmt.Sprintf("clean up deleted Server Profile %q: %v", profileID, cleanupErr))
		}
	}()
}

func (a *App) serverProfiles() clientprofile.State {
	if a.profiles == nil {
		return clientprofile.State{Version: 1, Profiles: []clientprofile.Profile{}}
	}
	return a.profiles.Snapshot()
}
