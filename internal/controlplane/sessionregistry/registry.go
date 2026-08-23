package sessionregistry

import (
	"context"
	"errors"
	"strings"
	"sync"
)

var ErrClosed = errors.New("session runtime registry is closed")

type Registry struct {
	root       context.Context
	cancelRoot context.CancelCauseFunc

	mu       sync.Mutex
	closed   bool
	sequence uint64
	sessions map[string]*sessionNode
}

type sessionNode struct {
	id       string
	sequence uint64
	ctx      context.Context
	cancel   context.CancelCauseFunc
	draining bool
	tasks    map[string]*taskNode
	done     chan struct{}
}

type taskNode struct {
	id       string
	sequence uint64
	ctx      context.Context
	cancel   context.CancelCauseFunc
	streams  map[uint64]*streamNode
}

type streamNode struct {
	sequence   uint64
	cancel     context.CancelCauseFunc
	stopParent func() bool
	done       chan struct{}
	release    sync.Once
}

func New(parent context.Context) *Registry {
	if parent == nil {
		parent = context.Background()
	}
	root, cancel := context.WithCancelCause(parent)
	return &Registry{
		root:       root,
		cancelRoot: cancel,
		sessions:   make(map[string]*sessionNode),
	}
}

func (registry *Registry) Ensure(sessionID string) error {
	if !validID(sessionID) {
		return errors.New("session runtime ID is invalid")
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.closed || registry.root.Err() != nil {
		return ErrClosed
	}
	_, err := registry.ensureSessionLocked(sessionID)
	return err
}

// Attach creates the Session -> Task -> Stream context path. The returned
// release function must run after the stream has closed listeners, Kubernetes
// resources and sockets; Disconnect and Shutdown wait for that release.
func (registry *Registry) Attach(
	parent context.Context,
	sessionID, taskID string,
) (context.Context, func(), error) {
	if parent == nil || !validID(sessionID) || !validID(taskID) {
		return nil, nil, errors.New(
			"session and Task runtime identities are required",
		)
	}
	registry.mu.Lock()
	if registry.closed || registry.root.Err() != nil {
		registry.mu.Unlock()
		return nil, nil, ErrClosed
	}
	session, err := registry.ensureSessionLocked(sessionID)
	if err != nil || session.draining {
		registry.mu.Unlock()
		if err != nil {
			return nil, nil, err
		}
		return nil, nil, errors.New("session runtime is draining")
	}
	task := session.tasks[taskID]
	if task == nil {
		registry.sequence++
		taskContext, cancelTask := context.WithCancelCause(session.ctx)
		task = &taskNode{
			id: taskID, sequence: registry.sequence, ctx: taskContext,
			cancel: cancelTask, streams: make(map[uint64]*streamNode),
		}
		session.tasks[taskID] = task
	}
	registry.sequence++
	streamID := registry.sequence
	streamContext, cancelStream := context.WithCancelCause(task.ctx)
	stream := &streamNode{
		sequence: streamID,
		cancel:   cancelStream,
		done:     make(chan struct{}),
	}
	stream.stopParent = context.AfterFunc(
		parent,
		func() { cancelStream(context.Cause(parent)) },
	)
	task.streams[streamID] = stream
	registry.mu.Unlock()

	release := func() {
		stream.release.Do(func() {
			stream.stopParent()
			stream.cancel(context.Canceled)
			registry.release(sessionID, taskID, streamID, stream)
		})
	}
	return streamContext, release, nil
}

func (registry *Registry) ensureSessionLocked(
	sessionID string,
) (*sessionNode, error) {
	if session := registry.sessions[sessionID]; session != nil {
		if session.draining {
			return nil, errors.New("session runtime is draining")
		}
		return session, nil
	}
	registry.sequence++
	ctx, cancel := context.WithCancelCause(registry.root)
	session := &sessionNode{
		id: sessionID, sequence: registry.sequence, ctx: ctx, cancel: cancel,
		tasks: make(map[string]*taskNode), done: make(chan struct{}),
	}
	registry.sessions[sessionID] = session
	return session, nil
}

func (registry *Registry) release(
	sessionID, taskID string,
	streamID uint64,
	stream *streamNode,
) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	session := registry.sessions[sessionID]
	if session == nil {
		close(stream.done)
		return
	}
	task := session.tasks[taskID]
	if task != nil && task.streams[streamID] == stream {
		delete(task.streams, streamID)
		close(stream.done)
		if len(task.streams) == 0 {
			task.cancel(context.Canceled)
			delete(session.tasks, taskID)
		}
	}
	registry.finishSessionLocked(session)
}

func validID(value string) bool {
	return value != "" && len(value) <= 256 && strings.TrimSpace(value) == value
}

func compareSequenceDescending(left, right uint64) int {
	switch {
	case left > right:
		return -1
	case left < right:
		return 1
	default:
		return 0
	}
}
