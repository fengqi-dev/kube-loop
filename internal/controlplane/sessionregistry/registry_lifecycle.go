package sessionregistry

import (
	"context"
	"errors"
	"slices"
)

func (registry *Registry) Disconnect(
	ctx context.Context,
	sessionID string,
) error {
	if ctx == nil || !validID(sessionID) {
		return errors.New(
			"session runtime identity and cleanup context are required",
		)
	}
	registry.mu.Lock()
	session := registry.sessions[sessionID]
	if session == nil {
		registry.mu.Unlock()
		return nil
	}
	registry.beginDrainLocked(session, errors.New("session disconnected"))
	done := session.done
	registry.mu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (registry *Registry) Shutdown(ctx context.Context) error {
	if ctx == nil {
		return errors.New("session registry shutdown context is required")
	}
	registry.mu.Lock()
	if !registry.closed {
		registry.closed = true
	}
	sessions := make([]*sessionNode, 0, len(registry.sessions))
	for _, session := range registry.sessions {
		registry.beginDrainLocked(session, ErrClosed)
		sessions = append(sessions, session)
	}
	registry.cancelRoot(ErrClosed)
	slices.SortFunc(sessions, func(left, right *sessionNode) int {
		return compareSequenceDescending(left.sequence, right.sequence)
	})
	registry.mu.Unlock()
	var result error
	for _, session := range sessions {
		select {
		case <-session.done:
		case <-ctx.Done():
			return errors.Join(result, ctx.Err())
		}
	}
	return result
}

func (registry *Registry) beginDrainLocked(session *sessionNode, cause error) {
	if session.draining {
		return
	}
	session.draining = true
	streams := make([]*streamNode, 0)
	tasks := make([]*taskNode, 0, len(session.tasks))
	for _, task := range session.tasks {
		tasks = append(tasks, task)
		for _, stream := range task.streams {
			streams = append(streams, stream)
		}
	}
	slices.SortFunc(streams, func(left, right *streamNode) int {
		return compareSequenceDescending(left.sequence, right.sequence)
	})
	for _, stream := range streams {
		stream.cancel(cause)
	}
	slices.SortFunc(tasks, func(left, right *taskNode) int {
		return compareSequenceDescending(left.sequence, right.sequence)
	})
	for _, task := range tasks {
		task.cancel(cause)
	}
	session.cancel(cause)
	registry.finishSessionLocked(session)
}

func (registry *Registry) finishSessionLocked(session *sessionNode) {
	if !session.draining || len(session.tasks) != 0 {
		return
	}
	if registry.sessions[session.id] == session {
		delete(registry.sessions, session.id)
		close(session.done)
	}
}
