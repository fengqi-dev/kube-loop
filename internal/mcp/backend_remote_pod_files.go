package mcp

import (
	"context"

	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
)

func (backend *RemoteBackend) ListPodFiles(
	ctx context.Context,
	identity TrafficIdentity,
	spec clientremote.PodFileSpec,
) (clientremote.PodFileList, error) {
	serverProfile, session, err := backend.requireSession(identity.ProfileID, identity.SessionID, identity.Namespace)
	if err != nil {
		return clientremote.PodFileList{}, err
	}
	return backend.dependencies.ControlPlane.ListPodFiles(ctx, serverProfile, session, spec)
}

func (backend *RemoteBackend) CreatePodFileOperation(
	ctx context.Context,
	identity TrafficIdentity,
	action string,
	spec clientremote.PodFileSpec,
	idempotencyKey string,
) (clientremote.PodFileTask, error) {
	serverProfile, session, err := backend.requireSession(identity.ProfileID, identity.SessionID, identity.Namespace)
	if err != nil {
		return clientremote.PodFileTask{}, err
	}
	return backend.dependencies.ControlPlane.CreatePodFileOperation(
		ctx, serverProfile, session, action, spec, idempotencyKey,
	)
}
