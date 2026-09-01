//nolint:dupl // Task endpoints intentionally share the same typed lifecycle shape.
package remote

import (
	"context"
	"net/http"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
)

func (client *Client) CreatePreview(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	spec PreviewSpec,
	idempotencyKey string,
) (PreviewTask, error) {
	return createRemoteTask(
		ctx, client, serverProfile, current, spec, idempotencyKey,
		"previews", "Preview idempotency key is invalid", "encode Preview request", 128,
		validatePreviewSpec, validatePreviewTask,
	)
}

func (client *Client) GetPreview(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	taskID string,
) (PreviewTask, error) {
	return remoteTaskByID(
		ctx, client, serverProfile, current, taskID, http.MethodGet,
		"previews", "Preview Task ID is invalid", validatePreviewTask,
	)
}

func (client *Client) ListPreviews(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
) ([]PreviewTask, error) {
	return listRemoteTasks(ctx, client, serverProfile, current, "previews", validatePreviewTask)
}

func (client *Client) PausePreview(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	taskID string,
) (PreviewTask, error) {
	return remoteTaskAction(
		ctx, client, serverProfile, current, taskID, http.MethodPost,
		"previews", "pause", "Preview Task ID is invalid", validatePreviewTask,
	)
}

// StopPreview is retained for internal compatibility and deletes the task.
func (client *Client) StopPreview(
	ctx context.Context, serverProfile profile.Profile, current Session, taskID string,
) (PreviewTask, error) {
	return client.DeletePreview(ctx, serverProfile, current, taskID)
}

func (client *Client) ResumePreview(
	ctx context.Context, serverProfile profile.Profile, current Session, taskID string,
) (PreviewTask, error) {
	return remoteTaskAction(
		ctx, client, serverProfile, current, taskID, http.MethodPost,
		"previews", "resume", "Preview Task ID is invalid", validatePreviewTask,
	)
}

func (client *Client) DeletePreview(
	ctx context.Context, serverProfile profile.Profile, current Session, taskID string,
) (PreviewTask, error) {
	return remoteTaskAction(
		ctx, client, serverProfile, current, taskID, http.MethodDelete,
		"previews", "", "Preview Task ID is invalid", validatePreviewTask,
	)
}
