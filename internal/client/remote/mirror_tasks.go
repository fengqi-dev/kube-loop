package remote

import (
	"context"
	"net/http"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
)

func (client *Client) CreateMirror(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	spec MirrorSpec,
	idempotencyKey string,
) (MirrorTask, error) {
	return createRemoteTask(
		ctx, client, serverProfile, current, spec, idempotencyKey,
		"mirrors", "Mirror idempotency key is invalid", "encode Mirror request", 128,
		validateMirrorSpec, validateMirrorTask,
	)
}

func (client *Client) GetMirror(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	taskID string,
) (MirrorTask, error) {
	return remoteTaskByID(
		ctx, client, serverProfile, current, taskID, http.MethodGet,
		"mirrors", "Mirror Task ID is invalid", validateMirrorTask,
	)
}

func (client *Client) StopMirror(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	taskID string,
) (MirrorTask, error) {
	return remoteTaskByID(
		ctx, client, serverProfile, current, taskID, http.MethodDelete,
		"mirrors", "Mirror Task ID is invalid", validateMirrorTask,
	)
}
