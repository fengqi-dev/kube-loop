package mcp

import (
	"context"
	"errors"
	"strings"

	clientprofile "github.com/fengqi-dev/kube-loop/internal/client/profile"
	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
)

func (backend *RemoteBackend) activeProfile(profileID string) (clientprofile.Profile, error) {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return clientprofile.Profile{}, invalid(fieldProfileID, "profileId is required")
	}
	state := backend.dependencies.Profiles.Snapshot()
	if state.ActiveProfileID == "" {
		return clientprofile.Profile{}, &ToolError{
			Code: ErrorUnauthenticated, Message: "select and sign in to a Server Profile",
		}
	}
	if profileID != state.ActiveProfileID {
		return clientprofile.Profile{}, &ToolError{
			Code: ErrorForbidden, Message: "MCP can access only the active Server Profile", Field: fieldProfileID,
		}
	}
	for _, serverProfile := range state.Profiles {
		if serverProfile.ID == profileID {
			return serverProfile, nil
		}
	}
	return clientprofile.Profile{}, &ToolError{Code: ErrorNotFound, Message: "active Server Profile was not found"}
}

func (backend *RemoteBackend) requireSession(
	profileID, sessionID, namespace string,
) (clientprofile.Profile, clientremote.Session, error) {
	serverProfile, err := backend.activeProfile(profileID)
	if err != nil {
		return clientprofile.Profile{}, clientremote.Session{}, err
	}
	sessionID, namespace = strings.TrimSpace(sessionID), strings.TrimSpace(namespace)
	if sessionID == "" {
		return clientprofile.Profile{}, clientremote.Session{}, invalid("sessionId", "sessionId is required")
	}
	if namespace == "" {
		return clientprofile.Profile{}, clientremote.Session{}, invalid("namespace", "namespace is required")
	}
	session, err := backend.dependencies.Sessions.Current(serverProfile.ID)
	if err != nil {
		return clientprofile.Profile{}, clientremote.Session{}, &ToolError{
			Code: ErrorConflict, Message: "active Cluster Session is required", cause: err,
		}
	}
	if session.ID != sessionID || session.Namespace != namespace || session.State != sessionStateActive {
		return clientprofile.Profile{}, clientremote.Session{}, &ToolError{
			Code: ErrorConflict, Message: "profileId, sessionId, and namespace must match the active Cluster Session",
		}
	}
	return serverProfile, session, nil
}

func (backend *RemoteBackend) stopLocalFeatures(ctx context.Context, profileID string) error {
	var result error
	if backend.dependencies.Files != nil {
		result = errors.Join(result, backend.dependencies.Files.StopProfile(profileID))
	}
	if backend.dependencies.ExecLifecycle != nil {
		result = errors.Join(result, backend.dependencies.ExecLifecycle.StopProfile(profileID))
	}
	if backend.dependencies.Forwards != nil {
		result = errors.Join(result, backend.dependencies.Forwards.StopProfile(ctx, profileID))
	}
	if backend.dependencies.Exchanges != nil {
		result = errors.Join(result, backend.dependencies.Exchanges.StopProfile(ctx, profileID))
	}
	if backend.dependencies.Mirrors != nil {
		result = errors.Join(result, backend.dependencies.Mirrors.StopProfile(ctx, profileID))
	}
	if backend.dependencies.Previews != nil {
		result = errors.Join(result, backend.dependencies.Previews.StopProfile(ctx, profileID))
	}
	return result
}
