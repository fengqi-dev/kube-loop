package remote

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/gorilla/websocket"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/protocol/filestream"
)

func (client *Client) CreateExecTask(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	spec ExecSpec,
	idempotencyKey string,
) (ExecTask, error) {
	if err := validateSessionTarget(current.Namespace, current.ID); err != nil || current.State != remoteSessionActive {
		return ExecTask{}, errors.New("active Session identity is required")
	}
	if err := validateExecSpec(spec); err != nil {
		return ExecTask{}, err
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return ExecTask{}, errors.New("pod exec idempotency key is required")
	}
	body, err := json.Marshal(spec)
	if err != nil {
		return ExecTask{}, errors.New("encode Pod exec request")
	}
	var result ExecTask
	if err := client.doJSONBody(
		ctx, serverProfile, http.MethodPost,
		"/api/sessions/"+url.PathEscape(current.ID)+"/exec",
		url.Values{remoteParamNamespace: {current.Namespace}},
		http.Header{remoteHeaderIdempotencyKey: {idempotencyKey}}, body, &result,
	); err != nil {
		return ExecTask{}, err
	}
	return validateExecTask(result, current)
}

func (client *Client) OpenExecStream(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	task ExecTask,
) (*websocket.Conn, error) {
	if _, err := validateExecTask(task, current); err != nil || task.State != remoteTaskPending {
		return nil, errors.New("pending Pod exec Task is required")
	}
	return client.openTaskWebSocket(ctx, serverProfile, current,
		"/api/sessions/"+url.PathEscape(current.ID)+"/exec/"+url.PathEscape(task.ID)+"/stream")
}

func (client *Client) CreateFileTransferTask(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	spec FileTransferSpec,
	idempotencyKey string,
) (FileTransferTask, error) {
	return createRemoteTask(
		ctx,
		client,
		serverProfile,
		current,
		spec,
		idempotencyKey,
		"file-transfers",
		"file transfer idempotency key is required",
		"encode file transfer request",
		0,
		validateFileTransferSpec,
		validateFileTransferTask,
	)
}

func (client *Client) GetFileTransferTask(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	taskID string,
) (FileTransferTask, error) {
	if err := validateSessionTarget(current.Namespace, current.ID); err != nil || current.State != remoteSessionActive {
		return FileTransferTask{}, errors.New("active Session identity is required")
	}
	if _, err := uuid.Parse(strings.TrimSpace(taskID)); err != nil {
		return FileTransferTask{}, errors.New("file transfer Task ID is invalid")
	}
	var result FileTransferTask
	if err := client.doJSON(
		ctx, serverProfile, http.MethodGet,
		"/api/sessions/"+url.PathEscape(current.ID)+"/file-transfers/"+url.PathEscape(taskID),
		url.Values{remoteParamNamespace: {current.Namespace}}, nil, &result,
	); err != nil {
		return FileTransferTask{}, err
	}
	return validateFileTransferTask(result, current)
}

func (client *Client) OpenFileTransferStream(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	task FileTransferTask,
) (*websocket.Conn, error) {
	if _, err := validateFileTransferTask(task, current); err != nil || task.State != remoteTaskPending {
		return nil, errors.New("pending file transfer Task is required")
	}
	connection, err := client.openTaskWebSocket(ctx, serverProfile, current,
		"/api/sessions/"+url.PathEscape(current.ID)+"/file-transfers/"+url.PathEscape(task.ID)+"/stream")
	if err == nil {
		connection.SetReadLimit(filestream.MaximumData + 1)
	}
	return connection, err
}

func (client *Client) ListPodFiles(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	spec PodFileSpec,
) (PodFileList, error) {
	if err := validateSessionTarget(current.Namespace, current.ID); err != nil || current.State != remoteSessionActive {
		return PodFileList{}, errors.New("active Session identity is required")
	}
	if err := validatePodFileSpec(remoteActionList, &spec); err != nil {
		return PodFileList{}, err
	}
	body, _ := json.Marshal(spec)
	var result PodFileList
	if err := client.doJSONBody(ctx, serverProfile, http.MethodPost,
		"/api/sessions/"+url.PathEscape(current.ID)+"/pod-files/list",
		url.Values{remoteParamNamespace: {current.Namespace}}, nil, body, &result); err != nil {
		return PodFileList{}, err
	}
	return validatePodFileList(result, current, spec)
}

func (client *Client) CreatePodFileOperation(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	action string,
	spec PodFileSpec,
	idempotencyKey string,
) (PodFileTask, error) {
	if err := validateSessionTarget(current.Namespace, current.ID); err != nil || current.State != remoteSessionActive {
		return PodFileTask{}, errors.New("active Session identity is required")
	}
	action = strings.ToLower(strings.TrimSpace(action))
	if err := validatePodFileSpec(action, &spec); err != nil {
		return PodFileTask{}, err
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" || len(idempotencyKey) > 128 {
		return PodFileTask{}, errors.New("remote file operation idempotency key is invalid")
	}
	body, _ := json.Marshal(spec)
	var result PodFileTask
	if err := client.doJSONBody(
		ctx,
		serverProfile,
		http.MethodPost,
		"/api/sessions/"+url.PathEscape(current.ID)+"/pod-files/"+action,
		url.Values{remoteParamNamespace: {current.Namespace}},
		http.Header{remoteHeaderIdempotencyKey: {idempotencyKey}},
		body,
		&result,
	); err != nil {
		return PodFileTask{}, err
	}
	return validatePodFileTask(result, current)
}

func (client *Client) GetPodFileOperation(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
	taskID string,
) (PodFileTask, error) {
	if err := validateSessionTarget(current.Namespace, current.ID); err != nil || current.State != remoteSessionActive {
		return PodFileTask{}, errors.New("active Session identity is required")
	}
	if _, err := uuid.Parse(strings.TrimSpace(taskID)); err != nil {
		return PodFileTask{}, errors.New("remote file operation Task ID is invalid")
	}
	var result PodFileTask
	if err := client.doJSON(ctx, serverProfile, http.MethodGet,
		"/api/sessions/"+url.PathEscape(current.ID)+"/pod-files/operations/"+url.PathEscape(taskID),
		url.Values{remoteParamNamespace: {current.Namespace}}, nil, &result); err != nil {
		return PodFileTask{}, err
	}
	return validatePodFileTask(result, current)
}
