package podssh

import (
	"context"
	"io"

	"k8s.io/client-go/tools/remotecommand"
)

const DefaultPort uint16 = 22

// Target identifies the container behind one virtual SSH endpoint.
type Target struct {
	Context    string   `json:"context"`
	Namespace  string   `json:"namespace"`
	Pod        string   `json:"pod"`
	Container  string   `json:"container"`
	Containers []string `json:"containers,omitempty"`
	IP         string   `json:"ip"`
}

// Info is the UI-facing state for one enabled Pod SSH endpoint.
type Info struct {
	ID        string `json:"id"`
	Context   string `json:"context"`
	Namespace string `json:"namespace"`
	Pod       string `json:"pod"`
	Container string `json:"container"`
	IP        string `json:"ip"`
	Port      uint16 `json:"port"`
	Command   string `json:"command"`
}

// EnableRequest selects a Pod and optionally one of its containers.
type EnableRequest struct {
	Context   string `json:"context"`
	Namespace string `json:"namespace"`
	Pod       string `json:"pod"`
	Container string `json:"container,omitempty"`
}

// Streams are attached to a Kubernetes remotecommand exec session.
type Streams struct {
	Stdin             io.Reader
	Stdout            io.Writer
	Stderr            io.Writer
	TTY               bool
	TerminalSizeQueue remotecommand.TerminalSizeQueue
}

// Executor is implemented by the Kubernetes cluster provider.
type Executor interface {
	Exec(context.Context, Target, []string, Streams) error
}

// PodRef is the live inventory subset exposed through default Pod SSH mappings.
type PodRef struct {
	Context    string
	Namespace  string
	Pod        string
	IP         string
	Containers []string
}
