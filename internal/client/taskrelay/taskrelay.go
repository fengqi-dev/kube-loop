// Package taskrelay owns what the desktop's traffic task managers -- Exchange,
// Mirror and Preview -- do identically: track the locally relayed tasks of
// every Server Profile, pause and resume them against the Gateway, and
// reconcile local relays toward the Gateway's TrafficBinding state.
//
// The three tasks differ only in how a relay is opened and how the client
// describes a task to the desktop UI, so those are the two hooks a caller
// supplies. Each manager keeps its own wire types: the desktop bindings expose
// them to the frontend, and their JSON must stay separate.
package taskrelay

import (
	"context"
	"errors"
	"io"
	"net"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/client/reverserelay"
)

// Target is one local endpoint a relay serves.
type Target = reverserelay.Target

// The local lifecycle of a relayed task. A task is Running while its relay is
// carrying traffic, Paused once the relay is gone, and Pausing in between.
const (
	StateRunning = "running"
	StatePausing = "pausing"
	StatePaused  = "paused"
)

// Task is the Gateway-side state of one durable traffic task, normalized
// across the three task types.
type Task struct {
	ID        string
	SessionID string
	Namespace string
	// Service names the Kubernetes Service. Preview calls it the Preview name.
	Service   string
	ClusterIP string
	// Running reports whether the Gateway considers the task running. Restore
	// reconciles the local relay toward it.
	Running bool
	Targets []Target
}

// Relay is the local end of one task's traffic stream.
type Relay interface {
	// ReadReady blocks until the Data Plane reports the relay may serve
	// traffic.
	ReadReady(context.Context) error
	// Run carries traffic until the context is cancelled or the stream ends.
	Run(context.Context) error
	// Stop asks the stream owner to end the relay.
	Stop(context.Context) error
}

// Gateway is the remote half of one task type. Each manager adapts its own
// remote client to it, because the client exposes a differently named method
// per task type.
type Gateway interface {
	// Pause releases the Gateway-side resources but keeps the durable task.
	Pause(context.Context, profile.Profile, remote.Session, string) error
	// Resume re-materializes a paused task and returns its new state.
	Resume(context.Context, profile.Profile, remote.Session, string) (Task, error)
	// Delete removes the durable task.
	Delete(context.Context, profile.Profile, remote.Session, string) error
	// List reports the tasks Restore may reconcile against: those in a state
	// the desktop can still own, already carrying the validated local targets
	// their relay will serve. A task the manager must not adopt is omitted
	// rather than reported.
	List(context.Context, profile.Profile, remote.Session) ([]Task, error)
}

// Open dials the Data Plane traffic stream for one task and wraps it in the
// local relay. The returned close function releases the stream when the relay
// never starts; once Run owns the relay the manager does not call it.
type Open func(context.Context, profile.Profile, Task) (Relay, func() error, error)

// Entry is what the manager knows about one locally managed task. Describe
// turns it into the manager's own wire document.
type Entry struct {
	ProfileID string
	Task      Task
	State     string
}

// ClosedStream reports whether err is the ordinary end of a traffic stream
// rather than a failure. A relay that has already been torn down by the far
// end reports one of these, and stopping it again is not an error.
func ClosedStream(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) ||
		errors.Is(err, net.ErrClosed)
}
