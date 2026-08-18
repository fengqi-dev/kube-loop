package mcp

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	clientdataplane "github.com/fengqi-dev/kube-loop/internal/client/dataplane"
	clientexchange "github.com/fengqi-dev/kube-loop/internal/client/exchange"
	clientexec "github.com/fengqi-dev/kube-loop/internal/client/exec"
	clientfiletransfer "github.com/fengqi-dev/kube-loop/internal/client/filetransfer"
	clientmirror "github.com/fengqi-dev/kube-loop/internal/client/mirror"
	clientportforward "github.com/fengqi-dev/kube-loop/internal/client/portforward"
	clientpreview "github.com/fengqi-dev/kube-loop/internal/client/preview"
	clientprofile "github.com/fengqi-dev/kube-loop/internal/client/profile"
	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/protocol/execstream"
)

const maximumCommandOutput = 1 << 20

type ProfileSource interface {
	Snapshot() clientprofile.State
}

type ControlPlaneClient interface {
	Version(context.Context, clientprofile.Profile) (clientremote.Version, error)
	Capabilities(context.Context, clientprofile.Profile, string) (clientremote.Capabilities, error)
	Namespaces(context.Context, clientprofile.Profile) ([]clientremote.Namespace, error)
	Pods(context.Context, clientprofile.Profile, string) ([]clientremote.Pod, error)
	Services(context.Context, clientprofile.Profile, string) ([]clientremote.Service, error)
	ListPodFiles(context.Context, clientprofile.Profile, clientremote.Session, clientremote.PodFileSpec) (clientremote.PodFileList, error)
	CreatePodFileOperation(context.Context, clientprofile.Profile, clientremote.Session, string, clientremote.PodFileSpec, string) (clientremote.PodFileTask, error)
}

type SessionManager interface {
	Connect(context.Context, clientprofile.Profile, string) (clientremote.Session, error)
	Current(string) (clientremote.Session, error)
	Disconnect(context.Context, string) error
}

type DataPlaneManager interface {
	Connect(context.Context, clientprofile.Profile, clientremote.Session) (clientdataplane.Status, error)
	Disconnect(string) error
}

type ExecLifecycle interface {
	StopProfile(string) error
}

type FileTransferManager interface {
	Start(clientprofile.Profile, clientremote.Session, clientfiletransfer.Request) (clientfiletransfer.Task, error)
	List(string) []clientfiletransfer.Task
	Cancel(string, string) error
	StopProfile(string) error
}

type PortForwardManager interface {
	Start(context.Context, clientprofile.Profile, clientremote.Session, clientportforward.Request) (clientportforward.Info, error)
	Stop(context.Context, string, string) error
	List(string) []clientportforward.Info
	StopProfile(context.Context, string) error
}

type ExchangeManager interface {
	Start(context.Context, clientprofile.Profile, clientremote.Session, clientexchange.Request) (clientexchange.Info, error)
	Stop(context.Context, string, string) error
	List(string) []clientexchange.Info
	StopProfile(context.Context, string) error
}

type MirrorManager interface {
	Start(context.Context, clientprofile.Profile, clientremote.Session, clientmirror.Request) (clientmirror.Info, error)
	Stop(context.Context, string, string) error
	List(string) []clientmirror.Info
	StopProfile(context.Context, string) error
}

type PreviewManager interface {
	Start(context.Context, clientprofile.Profile, clientremote.Session, clientpreview.Request) (clientpreview.Info, error)
	Stop(context.Context, string, string) error
	List(string) []clientpreview.Info
	StopProfile(context.Context, string) error
}

type RemoteDependencies struct {
	Profiles      ProfileSource
	ControlPlane  ControlPlaneClient
	Sessions      SessionManager
	DataPlanes    DataPlaneManager
	ExecClient    clientexec.Client
	ExecLifecycle ExecLifecycle
	Files         FileTransferManager
	Forwards      PortForwardManager
	Exchanges     ExchangeManager
	Mirrors       MirrorManager
	Previews      PreviewManager
}

// RemoteBackend is the only production MCP backend. It accepts only the
// currently active Profile and delegates cluster work to the client SDKs.
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

func (backend *RemoteBackend) Version(ctx context.Context, profileID string) (clientremote.Version, error) {
	serverProfile, err := backend.activeProfile(profileID)
	if err != nil {
		return clientremote.Version{}, err
	}
	return backend.dependencies.ControlPlane.Version(ctx, serverProfile)
}

func (backend *RemoteBackend) Capabilities(ctx context.Context, profileID, namespace string) (clientremote.Capabilities, error) {
	serverProfile, err := backend.activeProfile(profileID)
	if err != nil {
		return clientremote.Capabilities{}, err
	}
	return backend.dependencies.ControlPlane.Capabilities(ctx, serverProfile, strings.TrimSpace(namespace))
}

func (backend *RemoteBackend) Namespaces(ctx context.Context, profileID string) ([]clientremote.Namespace, error) {
	serverProfile, err := backend.activeProfile(profileID)
	if err != nil {
		return nil, err
	}
	return backend.dependencies.ControlPlane.Namespaces(ctx, serverProfile)
}

func (backend *RemoteBackend) Pods(ctx context.Context, profileID, namespace string) ([]clientremote.Pod, error) {
	serverProfile, err := backend.activeProfile(profileID)
	if err != nil {
		return nil, err
	}
	return backend.dependencies.ControlPlane.Pods(ctx, serverProfile, strings.TrimSpace(namespace))
}

func (backend *RemoteBackend) Services(ctx context.Context, profileID, namespace string) ([]clientremote.Service, error) {
	serverProfile, err := backend.activeProfile(profileID)
	if err != nil {
		return nil, err
	}
	return backend.dependencies.ControlPlane.Services(ctx, serverProfile, strings.TrimSpace(namespace))
}

func (backend *RemoteBackend) CurrentSession(profileID string) (clientremote.Session, error) {
	serverProfile, err := backend.activeProfile(profileID)
	if err != nil {
		return clientremote.Session{}, err
	}
	return backend.dependencies.Sessions.Current(serverProfile.ID)
}

func (backend *RemoteBackend) Connect(ctx context.Context, profileID, namespace string) (clientremote.Session, error) {
	backend.sessionMu.Lock()
	defer backend.sessionMu.Unlock()
	serverProfile, err := backend.activeProfile(profileID)
	if err != nil {
		return clientremote.Session{}, err
	}
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return clientremote.Session{}, invalid("namespace", "namespace is required")
	}
	if current, currentErr := backend.dependencies.Sessions.Current(serverProfile.ID); currentErr == nil && current.Namespace != namespace {
		if err := backend.stopLocalFeatures(ctx, serverProfile.ID); err != nil {
			return clientremote.Session{}, err
		}
		if err := backend.dependencies.DataPlanes.Disconnect(serverProfile.ID); err != nil {
			return clientremote.Session{}, err
		}
	}
	session, err := backend.dependencies.Sessions.Connect(ctx, serverProfile, namespace)
	if err != nil {
		return clientremote.Session{}, err
	}
	if _, err := backend.dependencies.DataPlanes.Connect(ctx, serverProfile, session); err != nil {
		_ = backend.dependencies.Sessions.Disconnect(ctx, serverProfile.ID)
		return clientremote.Session{}, err
	}
	return session, nil
}

func (backend *RemoteBackend) Disconnect(ctx context.Context, profileID, sessionID, namespace string) error {
	backend.sessionMu.Lock()
	defer backend.sessionMu.Unlock()
	serverProfile, session, err := backend.requireSession(profileID, sessionID, namespace)
	if err != nil {
		return err
	}
	featureErr := backend.stopLocalFeatures(ctx, serverProfile.ID)
	dataPlaneErr := backend.dependencies.DataPlanes.Disconnect(serverProfile.ID)
	sessionErr := backend.dependencies.Sessions.Disconnect(ctx, sessionProfileID(serverProfile, session))
	return errors.Join(featureErr, dataPlaneErr, sessionErr)
}

func sessionProfileID(serverProfile clientprofile.Profile, _ clientremote.Session) string {
	return serverProfile.ID
}

func (backend *RemoteBackend) StartTraffic(ctx context.Context, request TrafficStartRequest) (TrafficItem, error) {
	serverProfile, session, err := backend.requireSession(request.ProfileID, request.SessionID, request.Namespace)
	if err != nil {
		return TrafficItem{}, err
	}
	switch request.Type {
	case "exchange":
		if backend.dependencies.Exchanges == nil {
			return TrafficItem{}, &ToolError{Code: ErrorUnavailable, Message: "Exchange is unavailable"}
		}
		targets := make([]clientexchange.LocalTarget, len(request.Targets))
		for index, target := range request.Targets {
			targets[index] = clientexchange.LocalTarget(target)
		}
		info, err := backend.dependencies.Exchanges.Start(ctx, serverProfile, session, clientexchange.Request{
			ProfileID: serverProfile.ID, Service: request.Service, Targets: targets,
		})
		return TrafficItem{Type: request.Type, Exchange: &info}, err
	case "mirror":
		if backend.dependencies.Mirrors == nil {
			return TrafficItem{}, &ToolError{Code: ErrorUnavailable, Message: "Mirror is unavailable"}
		}
		targets := make([]clientmirror.LocalTarget, len(request.Targets))
		for index, target := range request.Targets {
			targets[index] = clientmirror.LocalTarget(target)
		}
		info, err := backend.dependencies.Mirrors.Start(ctx, serverProfile, session, clientmirror.Request{
			ProfileID: serverProfile.ID, Service: request.Service, Targets: targets,
		})
		return TrafficItem{Type: request.Type, Mirror: &info}, err
	case "preview":
		if backend.dependencies.Previews == nil {
			return TrafficItem{}, &ToolError{Code: ErrorUnavailable, Message: "Preview is unavailable"}
		}
		targets := make([]clientpreview.LocalTarget, len(request.Targets))
		for index, target := range request.Targets {
			targets[index] = clientpreview.LocalTarget(target)
		}
		info, err := backend.dependencies.Previews.Start(ctx, serverProfile, session, clientpreview.Request{
			ProfileID: serverProfile.ID, Namespace: session.Namespace, Name: request.Name, Targets: targets,
		})
		return TrafficItem{Type: request.Type, Preview: &info}, err
	case "port_forward":
		if backend.dependencies.Forwards == nil {
			return TrafficItem{}, &ToolError{Code: ErrorUnavailable, Message: "Port Forward is unavailable"}
		}
		info, err := backend.dependencies.Forwards.Start(ctx, serverProfile, session, clientportforward.Request{
			ProfileID: serverProfile.ID, Kind: request.TargetKind, Name: request.TargetName,
			Protocol: request.Protocol, RemotePort: request.RemotePort, LocalPort: request.LocalPort,
		})
		return TrafficItem{Type: request.Type, PortForward: &info}, err
	default:
		return TrafficItem{}, invalid("type", "type must be exchange, mirror, preview, or port_forward")
	}
}

func (backend *RemoteBackend) StopTraffic(ctx context.Context, identity TrafficIdentity) error {
	serverProfile, _, err := backend.requireSession(identity.ProfileID, identity.SessionID, identity.Namespace)
	if err != nil {
		return err
	}
	if strings.TrimSpace(identity.TaskID) == "" {
		return invalid("taskId", "taskId is required")
	}
	switch identity.Type {
	case "exchange":
		if backend.dependencies.Exchanges == nil || !matchesExchange(backend.dependencies.Exchanges.List(serverProfile.ID), identity) {
			return &ToolError{Code: ErrorNotFound, Message: "Exchange is not active"}
		}
		return backend.dependencies.Exchanges.Stop(ctx, serverProfile.ID, identity.TaskID)
	case "mirror":
		if backend.dependencies.Mirrors == nil || !matchesMirror(backend.dependencies.Mirrors.List(serverProfile.ID), identity) {
			return &ToolError{Code: ErrorNotFound, Message: "Mirror is not active"}
		}
		return backend.dependencies.Mirrors.Stop(ctx, serverProfile.ID, identity.TaskID)
	case "preview":
		if backend.dependencies.Previews == nil || !matchesPreview(backend.dependencies.Previews.List(serverProfile.ID), identity) {
			return &ToolError{Code: ErrorNotFound, Message: "Preview is not active"}
		}
		return backend.dependencies.Previews.Stop(ctx, serverProfile.ID, identity.TaskID)
	case "port_forward":
		if backend.dependencies.Forwards == nil || !matchesForward(backend.dependencies.Forwards.List(serverProfile.ID), identity) {
			return &ToolError{Code: ErrorNotFound, Message: "Port Forward is not active"}
		}
		return backend.dependencies.Forwards.Stop(ctx, serverProfile.ID, identity.TaskID)
	default:
		return invalid("type", "type must be exchange, mirror, preview, or port_forward")
	}
}

func (backend *RemoteBackend) ListTraffic(profileID, trafficType string) ([]TrafficItem, error) {
	serverProfile, err := backend.activeProfile(profileID)
	if err != nil {
		return nil, err
	}
	items := make([]TrafficItem, 0)
	if trafficType == "" || trafficType == "exchange" {
		if backend.dependencies.Exchanges != nil {
			for _, info := range backend.dependencies.Exchanges.List(serverProfile.ID) {
				copy := info
				items = append(items, TrafficItem{Type: "exchange", Exchange: &copy})
			}
		}
	}
	if trafficType == "" || trafficType == "mirror" {
		if backend.dependencies.Mirrors != nil {
			for _, info := range backend.dependencies.Mirrors.List(serverProfile.ID) {
				copy := info
				items = append(items, TrafficItem{Type: "mirror", Mirror: &copy})
			}
		}
	}
	if trafficType == "" || trafficType == "preview" {
		if backend.dependencies.Previews != nil {
			for _, info := range backend.dependencies.Previews.List(serverProfile.ID) {
				copy := info
				items = append(items, TrafficItem{Type: "preview", Preview: &copy})
			}
		}
	}
	if trafficType == "" || trafficType == "port_forward" {
		if backend.dependencies.Forwards != nil {
			for _, info := range backend.dependencies.Forwards.List(serverProfile.ID) {
				copy := info
				items = append(items, TrafficItem{Type: "port_forward", PortForward: &copy})
			}
		}
	}
	slices.SortFunc(items, func(left, right TrafficItem) int {
		return strings.Compare(trafficItemID(left), trafficItemID(right))
	})
	return items, nil
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
	stream, err := clientexec.Start(commandContext, backend.dependencies.ExecClient, serverProfile, session, clientremote.ExecSpec{
		Pod: strings.TrimSpace(request.Pod), Container: strings.TrimSpace(request.Container),
		Command: append([]string(nil), request.Command...), TTY: false,
	})
	if err != nil {
		return PodCommandResult{}, err
	}
	defer stream.Close()
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

func (backend *RemoteBackend) StartFileTransfer(identity TrafficIdentity, request clientfiletransfer.Request) (clientfiletransfer.Task, error) {
	if backend.dependencies.Files == nil {
		return clientfiletransfer.Task{}, &ToolError{Code: ErrorUnavailable, Message: "file transfer is unavailable"}
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
		return nil, &ToolError{Code: ErrorUnavailable, Message: "file transfer is unavailable"}
	}
	return backend.dependencies.Files.List(serverProfile.ID), nil
}

func (backend *RemoteBackend) CancelFileTransfer(identity TrafficIdentity) error {
	serverProfile, _, err := backend.requireSession(identity.ProfileID, identity.SessionID, identity.Namespace)
	if err != nil {
		return err
	}
	if backend.dependencies.Files == nil {
		return &ToolError{Code: ErrorUnavailable, Message: "file transfer is unavailable"}
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
		return clientprofile.Profile{}, invalid("profileId", "profileId is required")
	}
	state := backend.dependencies.Profiles.Snapshot()
	if state.ActiveProfileID == "" {
		return clientprofile.Profile{}, &ToolError{Code: ErrorUnauthenticated, Message: "select and sign in to a Server Profile"}
	}
	if profileID != state.ActiveProfileID {
		return clientprofile.Profile{}, &ToolError{
			Code: ErrorForbidden, Message: "MCP can access only the active Server Profile", Field: "profileId",
		}
	}
	for _, serverProfile := range state.Profiles {
		if serverProfile.ID == profileID {
			return serverProfile, nil
		}
	}
	return clientprofile.Profile{}, &ToolError{Code: ErrorNotFound, Message: "active Server Profile was not found"}
}

func (backend *RemoteBackend) requireSession(profileID, sessionID, namespace string) (clientprofile.Profile, clientremote.Session, error) {
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
		return clientprofile.Profile{}, clientremote.Session{}, &ToolError{Code: ErrorConflict, Message: "active Cluster Session is required", cause: err}
	}
	if session.ID != sessionID || session.Namespace != namespace || session.State != "active" {
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

func matchesExchange(items []clientexchange.Info, identity TrafficIdentity) bool {
	for _, item := range items {
		if item.ID == identity.TaskID && item.SessionID == identity.SessionID && item.Namespace == identity.Namespace {
			return true
		}
	}
	return false
}

func matchesMirror(items []clientmirror.Info, identity TrafficIdentity) bool {
	for _, item := range items {
		if item.ID == identity.TaskID && item.SessionID == identity.SessionID && item.Namespace == identity.Namespace {
			return true
		}
	}
	return false
}

func matchesPreview(items []clientpreview.Info, identity TrafficIdentity) bool {
	for _, item := range items {
		if item.ID == identity.TaskID && item.SessionID == identity.SessionID && item.Namespace == identity.Namespace {
			return true
		}
	}
	return false
}

func matchesForward(items []clientportforward.Info, identity TrafficIdentity) bool {
	for _, item := range items {
		if item.ID == identity.TaskID && item.SessionID == identity.SessionID && item.Namespace == identity.Namespace {
			return true
		}
	}
	return false
}

func trafficItemID(item TrafficItem) string {
	switch {
	case item.Exchange != nil:
		return item.Exchange.ID
	case item.Mirror != nil:
		return item.Mirror.ID
	case item.Preview != nil:
		return item.Preview.ID
	case item.PortForward != nil:
		return item.PortForward.ID
	default:
		return ""
	}
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
