package remote

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
)

func createRemoteTask[Spec, Task any](
	ctx context.Context,
	client *Client,
	serverProfile profile.Profile,
	current Session,
	spec Spec,
	idempotencyKey,
	resource,
	idempotencyError,
	encodeError string,
	maximumKeyLength int,
	validateSpec func(*Spec) error,
	validateTask func(Task, Session) (Task, error),
) (Task, error) {
	var zero Task
	if err := validateSessionTarget(current.Namespace, current.ID); err != nil || current.State != remoteSessionActive {
		return zero, errors.New("active Session identity is required")
	}
	if err := validateSpec(&spec); err != nil {
		return zero, err
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" || maximumKeyLength > 0 && len(idempotencyKey) > maximumKeyLength {
		return zero, errors.New(idempotencyError)
	}
	body, err := json.Marshal(spec)
	if err != nil {
		return zero, errors.New(encodeError)
	}
	var result Task
	if err := client.doJSONBody(
		ctx,
		serverProfile,
		http.MethodPost,
		"/api/sessions/"+url.PathEscape(current.ID)+"/"+resource,
		url.Values{remoteParamNamespace: {current.Namespace}},
		http.Header{remoteHeaderIdempotencyKey: {idempotencyKey}},
		body,
		&result,
	); err != nil {
		return zero, err
	}
	return validateTask(result, current)
}

func (client *Client) CreatePortForward(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	spec PortForwardSpec,
	idempotencyKey string,
) (PortForwardTask, error) {
	return createRemoteTask(
		ctx,
		client,
		serverProfile,
		current,
		spec,
		idempotencyKey,
		"port-forwards",
		"Port Forward idempotency key is required",
		"encode Port Forward request",
		0,
		validatePortForwardSpec,
		validatePortForwardTask,
	)
}

func (client *Client) ListPortForwards(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
) ([]PortForwardTask, error) {
	if err := validateSessionTarget(current.Namespace, current.ID); err != nil || current.State != remoteSessionActive {
		return nil, errors.New("active Session identity is required")
	}
	var result struct {
		Items []PortForwardTask `json:"items"`
	}
	if err := client.doJSON(
		ctx, serverProfile, http.MethodGet,
		"/api/sessions/"+url.PathEscape(current.ID)+"/port-forwards",
		url.Values{remoteParamNamespace: {current.Namespace}}, nil, &result,
	); err != nil {
		return nil, err
	}
	if result.Items == nil {
		result.Items = []PortForwardTask{}
	}
	for index := range result.Items {
		validated, err := validatePortForwardTask(result.Items[index], current)
		if err != nil {
			return nil, err
		}
		result.Items[index] = validated
	}
	return result.Items, nil
}

func remoteTaskByID[Task any](
	ctx context.Context,
	client *Client,
	serverProfile profile.Profile,
	current Session,
	taskID,
	method,
	resource,
	invalidIDError string,
	validateTask func(Task, Session) (Task, error),
) (Task, error) {
	var zero Task
	if err := validateSessionTarget(current.Namespace, current.ID); err != nil || current.State != remoteSessionActive {
		return zero, errors.New("active Session identity is required")
	}
	taskID = strings.TrimSpace(taskID)
	if _, err := uuid.Parse(taskID); err != nil {
		return zero, errors.New(invalidIDError)
	}
	var result Task
	if err := client.doJSON(
		ctx,
		serverProfile,
		method,
		"/api/sessions/"+url.PathEscape(current.ID)+"/"+resource+"/"+url.PathEscape(taskID),
		url.Values{remoteParamNamespace: {current.Namespace}},
		nil,
		&result,
	); err != nil {
		return zero, err
	}
	return validateTask(result, current)
}

func (client *Client) StopPortForward(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	taskID string,
) (PortForwardTask, error) {
	if err := validateSessionTarget(current.Namespace, current.ID); err != nil || current.State != remoteSessionActive {
		return PortForwardTask{}, errors.New("active Session identity is required")
	}
	if _, err := uuid.Parse(strings.TrimSpace(taskID)); err != nil {
		return PortForwardTask{}, errors.New("port Forward Task ID is invalid")
	}
	var result PortForwardTask
	if err := client.doJSON(
		ctx, serverProfile, http.MethodDelete,
		"/api/sessions/"+url.PathEscape(current.ID)+"/port-forwards/"+url.PathEscape(taskID),
		url.Values{remoteParamNamespace: {current.Namespace}}, nil, &result,
	); err != nil {
		return PortForwardTask{}, err
	}
	return validatePortForwardTask(result, current)
}

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

func (client *Client) StopExchange(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	taskID string,
) (ExchangeTask, error) {
	return remoteTaskByID(
		ctx, client, serverProfile, current, taskID, http.MethodDelete,
		"exchanges", "Exchange Task ID is invalid", validateExchangeTask,
	)
}

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
