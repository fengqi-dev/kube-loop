package mcp

import (
	"context"

	clientdataplane "github.com/fengqi-dev/kube-loop/internal/client/dataplane"
	clientexchange "github.com/fengqi-dev/kube-loop/internal/client/exchange"
	clientexec "github.com/fengqi-dev/kube-loop/internal/client/exec"
	clientfiletransfer "github.com/fengqi-dev/kube-loop/internal/client/filetransfer"
	clientmirror "github.com/fengqi-dev/kube-loop/internal/client/mirror"
	clientportforward "github.com/fengqi-dev/kube-loop/internal/client/portforward"
	clientpreview "github.com/fengqi-dev/kube-loop/internal/client/preview"
	clientprofile "github.com/fengqi-dev/kube-loop/internal/client/profile"
	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
)

type ProfileSource interface {
	Snapshot() clientprofile.State
}

type ControlPlaneClient interface {
	Version(context.Context, clientprofile.Profile) (clientremote.Version, error)
	Capabilities(context.Context, clientprofile.Profile, string) (clientremote.Capabilities, error)
	Namespaces(context.Context, clientprofile.Profile) ([]clientremote.Namespace, error)
	Pods(context.Context, clientprofile.Profile, string) ([]clientremote.Pod, error)
	Services(context.Context, clientprofile.Profile, string) ([]clientremote.Service, error)
	ListPodFiles(
		context.Context,
		clientprofile.Profile,
		clientremote.Session,
		clientremote.PodFileSpec,
	) (clientremote.PodFileList, error)
	CreatePodFileOperation(
		context.Context,
		clientprofile.Profile,
		clientremote.Session,
		string,
		clientremote.PodFileSpec,
		string,
	) (clientremote.PodFileTask, error)
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
	Start(
		context.Context,
		clientprofile.Profile,
		clientremote.Session,
		clientportforward.Request,
	) (clientportforward.Info, error)
	Pause(context.Context, string, string) error
	Resume(context.Context, string, string) (clientportforward.Info, error)
	Delete(context.Context, string, string) error
	List(string) []clientportforward.Info
	StopProfile(context.Context, string) error
}

type ExchangeManager interface {
	Start(
		context.Context,
		clientprofile.Profile,
		clientremote.Session,
		clientexchange.Request,
	) (clientexchange.Info, error)
	Pause(context.Context, string, string) error
	Resume(context.Context, string, string) (clientexchange.Info, error)
	Delete(context.Context, string, string) error
	List(string) []clientexchange.Info
	StopProfile(context.Context, string) error
}

type MirrorManager interface {
	Start(context.Context, clientprofile.Profile, clientremote.Session, clientmirror.Request) (clientmirror.Info, error)
	Pause(context.Context, string, string) error
	Resume(context.Context, string, string) (clientmirror.Info, error)
	Delete(context.Context, string, string) error
	List(string) []clientmirror.Info
	StopProfile(context.Context, string) error
}

type PreviewManager interface {
	Start(
		context.Context,
		clientprofile.Profile,
		clientremote.Session,
		clientpreview.Request,
	) (clientpreview.Info, error)
	Pause(context.Context, string, string) error
	Resume(context.Context, string, string) (clientpreview.Info, error)
	Delete(context.Context, string, string) error
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
