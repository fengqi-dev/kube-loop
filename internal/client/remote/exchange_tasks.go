//nolint:dupl // Task endpoints intentionally share the same typed lifecycle shape.
package remote

import (
	"context"
	"net/http"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
)

func (client *Client) CreateExchange(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	spec ExchangeSpec,
	idempotencyKey string,
) (ExchangeTask, error) {
	return createRemoteTask(
		ctx, client, serverProfile, current, spec, idempotencyKey,
		"exchanges", "Exchange idempotency key is invalid", "encode Exchange request", 128,
		validateExchangeSpec, validateExchangeTask,
	)
}

func (client *Client) GetExchange(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	taskID string,
) (ExchangeTask, error) {
	return remoteTaskByID(
		ctx, client, serverProfile, current, taskID, http.MethodGet,
		"exchanges", "Exchange Task ID is invalid", validateExchangeTask,
	)
}

func (client *Client) ListExchanges(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
) ([]ExchangeTask, error) {
	return listRemoteTasks(ctx, client, serverProfile, current, "exchanges", validateExchangeTask)
}

func (client *Client) PauseExchange(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	taskID string,
) (ExchangeTask, error) {
	return remoteTaskAction(
		ctx, client, serverProfile, current, taskID, http.MethodPost,
		"exchanges", "pause", "Exchange Task ID is invalid", validateExchangeTask,
	)
}

// StopExchange is retained for internal compatibility and deletes the task.
func (client *Client) StopExchange(
	ctx context.Context, serverProfile profile.Profile, current Session, taskID string,
) (ExchangeTask, error) {
	return client.DeleteExchange(ctx, serverProfile, current, taskID)
}

func (client *Client) ResumeExchange(
	ctx context.Context, serverProfile profile.Profile, current Session, taskID string,
) (ExchangeTask, error) {
	return remoteTaskAction(
		ctx, client, serverProfile, current, taskID, http.MethodPost,
		"exchanges", "resume", "Exchange Task ID is invalid", validateExchangeTask,
	)
}

func (client *Client) DeleteExchange(
	ctx context.Context, serverProfile profile.Profile, current Session, taskID string,
) (ExchangeTask, error) {
	return remoteTaskAction(
		ctx, client, serverProfile, current, taskID, http.MethodDelete,
		"exchanges", "", "Exchange Task ID is invalid", validateExchangeTask,
	)
}
