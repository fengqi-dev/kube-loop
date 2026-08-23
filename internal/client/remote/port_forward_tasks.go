package remote

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
)

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
