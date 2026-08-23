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

func (client *Client) StopPreview(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	taskID string,
) (PreviewTask, error) {
	return remoteTaskByID(
		ctx, client, serverProfile, current, taskID, http.MethodDelete,
		"previews", "Preview Task ID is invalid", validatePreviewTask,
	)
}
