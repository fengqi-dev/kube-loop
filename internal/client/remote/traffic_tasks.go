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

func remoteTaskAction[Task any](
	ctx context.Context,
	client *Client,
	serverProfile profile.Profile,
	current Session,
	taskID,
	method,
	resource,
	action,
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
	path := "/api/sessions/" + url.PathEscape(current.ID) + "/" + resource + "/" + url.PathEscape(taskID)
	if action != "" {
		path += "/" + url.PathEscape(action)
	}
	var result Task
	if err := client.doJSON(
		ctx,
		serverProfile,
		method,
		path,
		url.Values{remoteParamNamespace: {current.Namespace}},
		nil,
		&result,
	); err != nil {
		return zero, err
	}
	return validateTask(result, current)
}
