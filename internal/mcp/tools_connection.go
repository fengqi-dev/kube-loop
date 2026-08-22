package mcp

import (
	"context"
	"strings"

	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
)

func manageConnection(ctx context.Context, backend Backend, input manageConnectionIn) (manageConnectionOut, error) {
	input.Action, input.ProfileID = strings.TrimSpace(input.Action), strings.TrimSpace(input.ProfileID)
	if input.ProfileID == "" {
		return manageConnectionOut{}, invalid("profileId", "profileId is required")
	}
	output := manageConnectionOut{Action: input.Action, ProfileID: input.ProfileID}
	var (
		session clientremote.Session
		err     error
	)
	switch input.Action {
	case "status":
		session, err = backend.CurrentSession(input.ProfileID)
	case actionConnect:
		if strings.TrimSpace(input.Namespace) == "" {
			return manageConnectionOut{}, invalid(resourceNamespace, "namespace is required for connect")
		}
		session, err = backend.Connect(ctx, input.ProfileID, input.Namespace)
	case actionDisconnect:
		if strings.TrimSpace(input.SessionID) == "" {
			return manageConnectionOut{}, invalid("sessionId", "sessionId is required for disconnect")
		}
		if strings.TrimSpace(input.Namespace) == "" {
			return manageConnectionOut{}, invalid(resourceNamespace, "namespace is required for disconnect")
		}
		current, currentErr := backend.CurrentSession(input.ProfileID)
		if currentErr != nil {
			return manageConnectionOut{}, currentErr
		}
		err = backend.Disconnect(ctx, input.ProfileID, input.SessionID, input.Namespace)
		session = current
		if err == nil {
			session.State = sessionStateStopped
		}
	default:
		return manageConnectionOut{}, invalid("action", "action must be status, connect, or disconnect")
	}
	output.Session = session
	return output, err
}
