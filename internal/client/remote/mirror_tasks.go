//nolint:dupl // Task endpoints intentionally share the same typed lifecycle shape.
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

func (client *Client) ListMirrors(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
) ([]MirrorTask, error) {
	return listRemoteTasks(ctx, client, serverProfile, current, "mirrors", validateMirrorTask)
}

func (client *Client) PauseMirror(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	taskID string,
) (MirrorTask, error) {
	return remoteTaskAction(
		ctx, client, serverProfile, current, taskID, http.MethodPost,
		"mirrors", "pause", "Mirror Task ID is invalid", validateMirrorTask,
	)
}

// StopMirror is retained for internal compatibility and deletes the task.
func (client *Client) StopMirror(
	ctx context.Context, serverProfile profile.Profile, current Session, taskID string,
) (MirrorTask, error) {
	return client.DeleteMirror(ctx, serverProfile, current, taskID)
}

func (client *Client) ResumeMirror(
	ctx context.Context, serverProfile profile.Profile, current Session, taskID string,
) (MirrorTask, error) {
	return remoteTaskAction(
		ctx, client, serverProfile, current, taskID, http.MethodPost,
		"mirrors", "resume", "Mirror Task ID is invalid", validateMirrorTask,
	)
}

func (client *Client) DeleteMirror(
	ctx context.Context, serverProfile profile.Profile, current Session, taskID string,
) (MirrorTask, error) {
	return remoteTaskAction(
		ctx, client, serverProfile, current, taskID, http.MethodDelete,
		"mirrors", "", "Mirror Task ID is invalid", validateMirrorTask,
	)
}
