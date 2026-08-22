package mcp

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	clientexec "github.com/fengqi-dev/kube-loop/internal/client/exec"
	clientfiletransfer "github.com/fengqi-dev/kube-loop/internal/client/filetransfer"
	clientprofile "github.com/fengqi-dev/kube-loop/internal/client/profile"
	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/protocol/execstream"
)

const maximumCommandOutput = 1 << 20

type RemoteBackend struct {
	dependencies RemoteDependencies
	sessionMu    sync.Mutex
}

func NewRemoteBackend(dependencies RemoteDependencies) (*RemoteBackend, error) {
	if dependencies.Profiles == nil || dependencies.ControlPlane == nil || dependencies.Sessions == nil ||
		dependencies.DataPlanes == nil || dependencies.ExecClient == nil {
		return nil, errors.New("MCP Profile, Control Plane, Session, Data Plane, and exec clients are required")
	}
	return &RemoteBackend{dependencies: dependencies}, nil
}

func (backend *RemoteBackend) ExecPodCommand(ctx context.Context, request PodCommandRequest) (PodCommandResult, error) {
	serverProfile, session, err := backend.requireSession(request.ProfileID, request.SessionID, request.Namespace)
	if err != nil {
		return PodCommandResult{}, err
	}
	timeout := request.TimeoutSeconds
	if timeout == 0 {
		timeout = 30
	}
	if timeout < 1 || timeout > 300 {
		return PodCommandResult{}, invalid("timeoutSeconds", "timeoutSeconds must be between 1 and 300")
	}
	commandContext, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	stream, err := clientexec.Start(
		commandContext,
		backend.dependencies.ExecClient,
		serverProfile,
		session,
		clientremote.ExecSpec{
			Pod: strings.TrimSpace(request.Pod), Container: strings.TrimSpace(request.Container),
			Command: append([]string(nil), request.Command...), TTY: false,
		},
	)
	if err != nil {
		return PodCommandResult{}, err
	}
	defer func() { _ = stream.Close() }()
	stdout, stderr := newCappedBuffer(maximumCommandOutput), newCappedBuffer(maximumCommandOutput)
	result := PodCommandResult{
		ProfileID: serverProfile.ID, SessionID: session.ID, Namespace: session.Namespace,
		TaskID: stream.Task().ID, Pod: stream.Task().Pod, Container: stream.Task().Container,
		Command: append([]string(nil), request.Command...),
	}
	for {
		frame, err := stream.Read(commandContext)
		if err != nil {
			return PodCommandResult{}, err
		}
		switch frame.Type {
		case execstream.Stdout:
			_, _ = stdout.Write(frame.Payload)
		case execstream.Stderr:
			_, _ = stderr.Write(frame.Payload)
		case execstream.Exit:
			status, err := execstream.DecodeExit(frame)
			if err != nil {
				return PodCommandResult{}, err
			}
			result.StdoutBase64 = base64.StdEncoding.EncodeToString(stdout.Bytes())
			result.StderrBase64 = base64.StdEncoding.EncodeToString(stderr.Bytes())
			result.StdoutTruncated, result.StderrTruncated = stdout.truncated, stderr.truncated
			result.ExitCode, result.Cancelled, result.Error = status.Code, status.Cancelled, status.Error
			return result, nil
		}
	}
}

func (backend *RemoteBackend) StartFileTransfer(
	identity TrafficIdentity,
	request clientfiletransfer.Request,
) (clientfiletransfer.Task, error) {
	if backend.dependencies.Files == nil {
		return clientfiletransfer.Task{}, &ToolError{Code: ErrorUnavailable, Message: fileTransferUnavailable}
	}
	serverProfile, session, err := backend.requireSession(identity.ProfileID, identity.SessionID, identity.Namespace)
	if err != nil {
		return clientfiletransfer.Task{}, err
	}
	request.ProfileID = serverProfile.ID
	return backend.dependencies.Files.Start(serverProfile, session, request)
}

func (backend *RemoteBackend) ListFileTransfers(profileID string) ([]clientfiletransfer.Task, error) {
	serverProfile, err := backend.activeProfile(profileID)
	if err != nil {
		return nil, err
	}
	if backend.dependencies.Files == nil {
		return nil, &ToolError{Code: ErrorUnavailable, Message: fileTransferUnavailable}
	}
	return backend.dependencies.Files.List(serverProfile.ID), nil
}

func (backend *RemoteBackend) CancelFileTransfer(identity TrafficIdentity) error {
	serverProfile, _, err := backend.requireSession(identity.ProfileID, identity.SessionID, identity.Namespace)
	if err != nil {
		return err
	}
	if backend.dependencies.Files == nil {
		return &ToolError{Code: ErrorUnavailable, Message: fileTransferUnavailable}
	}
	for _, task := range backend.dependencies.Files.List(serverProfile.ID) {
		if task.ID == identity.TaskID && task.SessionID == identity.SessionID && task.Namespace == identity.Namespace {
			return backend.dependencies.Files.Cancel(serverProfile.ID, identity.TaskID)
		}
	}
	return &ToolError{Code: ErrorNotFound, Message: "file transfer is not active"}
}

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

func (backend *RemoteBackend) activeProfile(profileID string) (clientprofile.Profile, error) {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return clientprofile.Profile{}, invalid(fieldProfileID, "profileId is required")
	}
	state := backend.dependencies.Profiles.Snapshot()
	if state.ActiveProfileID == "" {
		return clientprofile.Profile{}, &ToolError{
			Code: ErrorUnauthenticated, Message: "select and sign in to a Server Profile",
		}
	}
	if profileID != state.ActiveProfileID {
		return clientprofile.Profile{}, &ToolError{
			Code: ErrorForbidden, Message: "MCP can access only the active Server Profile", Field: fieldProfileID,
		}
	}
	for _, serverProfile := range state.Profiles {
		if serverProfile.ID == profileID {
			return serverProfile, nil
		}
	}
	return clientprofile.Profile{}, &ToolError{Code: ErrorNotFound, Message: "active Server Profile was not found"}
}

func (backend *RemoteBackend) requireSession(
	profileID, sessionID, namespace string,
) (clientprofile.Profile, clientremote.Session, error) {
	serverProfile, err := backend.activeProfile(profileID)
	if err != nil {
		return clientprofile.Profile{}, clientremote.Session{}, err
	}
	sessionID, namespace = strings.TrimSpace(sessionID), strings.TrimSpace(namespace)
	if sessionID == "" {
		return clientprofile.Profile{}, clientremote.Session{}, invalid("sessionId", "sessionId is required")
	}
	if namespace == "" {
		return clientprofile.Profile{}, clientremote.Session{}, invalid("namespace", "namespace is required")
	}
	session, err := backend.dependencies.Sessions.Current(serverProfile.ID)
	if err != nil {
		return clientprofile.Profile{}, clientremote.Session{}, &ToolError{
			Code: ErrorConflict, Message: "active Cluster Session is required", cause: err,
		}
	}
	if session.ID != sessionID || session.Namespace != namespace || session.State != sessionStateActive {
		return clientprofile.Profile{}, clientremote.Session{}, &ToolError{
			Code: ErrorConflict, Message: "profileId, sessionId, and namespace must match the active Cluster Session",
		}
	}
	return serverProfile, session, nil
}

func (backend *RemoteBackend) stopLocalFeatures(ctx context.Context, profileID string) error {
	var result error
	if backend.dependencies.Files != nil {
		result = errors.Join(result, backend.dependencies.Files.StopProfile(profileID))
	}
	if backend.dependencies.ExecLifecycle != nil {
		result = errors.Join(result, backend.dependencies.ExecLifecycle.StopProfile(profileID))
	}
	if backend.dependencies.Forwards != nil {
		result = errors.Join(result, backend.dependencies.Forwards.StopProfile(ctx, profileID))
	}
	if backend.dependencies.Exchanges != nil {
		result = errors.Join(result, backend.dependencies.Exchanges.StopProfile(ctx, profileID))
	}
	if backend.dependencies.Mirrors != nil {
		result = errors.Join(result, backend.dependencies.Mirrors.StopProfile(ctx, profileID))
	}
	if backend.dependencies.Previews != nil {
		result = errors.Join(result, backend.dependencies.Previews.StopProfile(ctx, profileID))
	}
	return result
}

type cappedBuffer struct {
	value     []byte
	limit     int
	truncated bool
}

func newCappedBuffer(limit int) *cappedBuffer { return &cappedBuffer{limit: limit} }

func (buffer *cappedBuffer) Write(value []byte) (int, error) {
	written := len(value)
	remaining := buffer.limit - len(buffer.value)
	if remaining > 0 {
		buffer.value = append(buffer.value, value[:min(remaining, len(value))]...)
	}
	if len(value) > remaining {
		buffer.truncated = true
	}
	return written, nil
}

func (buffer *cappedBuffer) Bytes() []byte { return append([]byte(nil), buffer.value...) }

var _ io.Writer = (*cappedBuffer)(nil)
