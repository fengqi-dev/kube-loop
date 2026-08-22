package mcp

import (
	"context"
	"errors"
	"strings"

	clientprofile "github.com/fengqi-dev/kube-loop/internal/client/profile"
	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
)

func (backend *RemoteBackend) Connect(ctx context.Context, profileID, namespace string) (clientremote.Session, error) {
	backend.sessionMu.Lock()
	defer backend.sessionMu.Unlock()
	serverProfile, err := backend.activeProfile(profileID)
	if err != nil {
		return clientremote.Session{}, err
	}
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return clientremote.Session{}, invalid("namespace", "namespace is required")
	}
	current, currentErr := backend.dependencies.Sessions.Current(serverProfile.ID)
	if currentErr == nil && current.Namespace != namespace {
		if err := backend.stopLocalFeatures(ctx, serverProfile.ID); err != nil {
			return clientremote.Session{}, err
		}
		if err := backend.dependencies.DataPlanes.Disconnect(serverProfile.ID); err != nil {
			return clientremote.Session{}, err
		}
	}
	session, err := backend.dependencies.Sessions.Connect(ctx, serverProfile, namespace)
	if err != nil {
		return clientremote.Session{}, err
	}
	if _, err := backend.dependencies.DataPlanes.Connect(ctx, serverProfile, session); err != nil {
		_ = backend.dependencies.Sessions.Disconnect(ctx, serverProfile.ID)
		return clientremote.Session{}, err
	}
	return session, nil
}

func (backend *RemoteBackend) Disconnect(ctx context.Context, profileID, sessionID, namespace string) error {
	backend.sessionMu.Lock()
	defer backend.sessionMu.Unlock()
	serverProfile, session, err := backend.requireSession(profileID, sessionID, namespace)
	if err != nil {
		return err
	}
	featureErr := backend.stopLocalFeatures(ctx, serverProfile.ID)
	dataPlaneErr := backend.dependencies.DataPlanes.Disconnect(serverProfile.ID)
	sessionErr := backend.dependencies.Sessions.Disconnect(ctx, sessionProfileID(serverProfile, session))
	return errors.Join(featureErr, dataPlaneErr, sessionErr)
}

func sessionProfileID(serverProfile clientprofile.Profile, _ clientremote.Session) string {
	return serverProfile.ID
}
