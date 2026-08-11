package mcp

import (
	"context"

	clientexchange "github.com/fengqi-dev/kube-loop/internal/client/exchange"
	clientfiletransfer "github.com/fengqi-dev/kube-loop/internal/client/filetransfer"
	clientmirror "github.com/fengqi-dev/kube-loop/internal/client/mirror"
	clientportforward "github.com/fengqi-dev/kube-loop/internal/client/portforward"
	clientpreview "github.com/fengqi-dev/kube-loop/internal/client/preview"
	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
)

type LocalTarget struct {
	ServicePort int32  `json:"servicePort"`
	Protocol    string `json:"protocol"`
	LocalHost   string `json:"localHost"`
	LocalPort   uint16 `json:"localPort"`
}

type TrafficStartRequest struct {
	Type       string
	ProfileID  string
	SessionID  string
	Namespace  string
	Service    string
	Name       string
	Targets    []LocalTarget
	TargetKind string
	TargetName string
	Protocol   string
	RemotePort uint16
	LocalPort  uint16
}

type TrafficIdentity struct {
	Type      string
	ProfileID string
	SessionID string
	Namespace string
	TaskID    string
}

type TrafficItem struct {
	Type        string                  `json:"type"`
	Exchange    *clientexchange.Info    `json:"exchange,omitempty"`
	Mirror      *clientmirror.Info      `json:"mirror,omitempty"`
	Preview     *clientpreview.Info     `json:"preview,omitempty"`
	PortForward *clientportforward.Info `json:"portForward,omitempty"`
}

type PodCommandRequest struct {
	ProfileID      string
	SessionID      string
	Namespace      string
	Pod            string
	Container      string
	Command        []string
	TimeoutSeconds int
}

type PodCommandResult struct {
	ProfileID       string   `json:"profileId"`
	SessionID       string   `json:"sessionId"`
	Namespace       string   `json:"namespace"`
	TaskID          string   `json:"taskId"`
	Pod             string   `json:"pod"`
	Container       string   `json:"container,omitempty"`
	Command         []string `json:"command"`
	Stdout          []byte   `json:"stdout"`
	Stderr          []byte   `json:"stderr"`
	ExitCode        uint32   `json:"exitCode"`
	Cancelled       bool     `json:"cancelled,omitempty"`
	Error           string   `json:"error,omitempty"`
	StdoutTruncated bool     `json:"stdoutTruncated,omitempty"`
	StderrTruncated bool     `json:"stderrTruncated,omitempty"`
}

type Backend interface {
	Version(context.Context, string) (clientremote.Version, error)
	Capabilities(context.Context, string, string) (clientremote.Capabilities, error)
	Namespaces(context.Context, string) ([]clientremote.Namespace, error)
	Pods(context.Context, string, string) ([]clientremote.Pod, error)
	Services(context.Context, string, string) ([]clientremote.Service, error)

	CurrentSession(string) (clientremote.Session, error)
	Connect(context.Context, string, string) (clientremote.Session, error)
	Disconnect(context.Context, string, string, string) error

	StartTraffic(context.Context, TrafficStartRequest) (TrafficItem, error)
	StopTraffic(context.Context, TrafficIdentity) error
	ListTraffic(string, string) ([]TrafficItem, error)

	ExecPodCommand(context.Context, PodCommandRequest) (PodCommandResult, error)

	StartFileTransfer(TrafficIdentity, clientfiletransfer.Request) (clientfiletransfer.Task, error)
	ListFileTransfers(string) ([]clientfiletransfer.Task, error)
	CancelFileTransfer(TrafficIdentity) error
}
