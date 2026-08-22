package mcp

import (
	"context"
	"strings"

	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
)

func (backend *RemoteBackend) Version(ctx context.Context, profileID string) (clientremote.Version, error) {
	serverProfile, err := backend.activeProfile(profileID)
	if err != nil {
		return clientremote.Version{}, err
	}
	return backend.dependencies.ControlPlane.Version(ctx, serverProfile)
}

func (backend *RemoteBackend) Capabilities(
	ctx context.Context,
	profileID, namespace string,
) (clientremote.Capabilities, error) {
	serverProfile, err := backend.activeProfile(profileID)
	if err != nil {
		return clientremote.Capabilities{}, err
	}
	return backend.dependencies.ControlPlane.Capabilities(ctx, serverProfile, strings.TrimSpace(namespace))
}

func (backend *RemoteBackend) Namespaces(ctx context.Context, profileID string) ([]clientremote.Namespace, error) {
	serverProfile, err := backend.activeProfile(profileID)
	if err != nil {
		return nil, err
	}
	return backend.dependencies.ControlPlane.Namespaces(ctx, serverProfile)
}

func (backend *RemoteBackend) Pods(ctx context.Context, profileID, namespace string) ([]clientremote.Pod, error) {
	serverProfile, err := backend.activeProfile(profileID)
	if err != nil {
		return nil, err
	}
	return backend.dependencies.ControlPlane.Pods(ctx, serverProfile, strings.TrimSpace(namespace))
}

func (backend *RemoteBackend) Services(
	ctx context.Context,
	profileID, namespace string,
) ([]clientremote.Service, error) {
	serverProfile, err := backend.activeProfile(profileID)
	if err != nil {
		return nil, err
	}
	return backend.dependencies.ControlPlane.Services(ctx, serverProfile, strings.TrimSpace(namespace))
}

func (backend *RemoteBackend) CurrentSession(profileID string) (clientremote.Session, error) {
	serverProfile, err := backend.activeProfile(profileID)
	if err != nil {
		return clientremote.Session{}, err
	}
	return backend.dependencies.Sessions.Current(serverProfile.ID)
}
