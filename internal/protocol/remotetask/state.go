package remotetask

import (
	"errors"
	"fmt"
	"slices"
)

type State string

const (
	Pending    State = "pending"
	Starting   State = "starting"
	Running    State = "running"
	Recovering State = "recovering"
	Failed     State = "failed"
	Stopping   State = "stopping"
	Stopped    State = "stopped"
	Deleted    State = "deleted"
)

var orderedStates = []State{Pending, Starting, Running, Recovering, Failed, Stopping, Stopped, Deleted}

func (state State) Valid() bool {
	switch state {
	case Pending, Starting, Running, Recovering, Failed, Stopping, Stopped, Deleted:
		return true
	default:
		return false
	}
}

func (state State) Terminal() bool {
	return state == Failed || state == Deleted
}

func (state State) Owned() bool {
	switch state {
	case Starting, Running, Recovering, Stopping:
		return true
	case Pending, Failed, Stopped, Deleted:
		return false
	}
	return false
}

func States() []State {
	return append([]State(nil), orderedStates...)
}

func ValidateTransition(current, next State) error {
	if !current.Valid() || !next.Valid() {
		return errors.New("remote Task transition contains an invalid state")
	}
	if current == next {
		if current.Terminal() || current == Pending || current == Stopped {
			return fmt.Errorf("remote Task state %q cannot heartbeat", current)
		}
		return nil
	}
	if !transitionAllowed(current, next) {
		return fmt.Errorf("remote Task transition %q -> %q is not allowed", current, next)
	}
	return nil
}

func transitionAllowed(current, next State) bool {
	var allowed []State
	switch current {
	case Pending:
		allowed = []State{Starting, Running, Stopping, Stopped, Failed, Deleted}
	case Starting:
		allowed = []State{Running, Stopping, Stopped, Failed, Recovering, Deleted}
	case Running:
		allowed = []State{Stopping, Stopped, Failed, Recovering, Deleted}
	case Stopping:
		allowed = []State{Stopped, Failed, Recovering, Deleted}
	case Recovering:
		allowed = []State{Stopping, Stopped, Failed, Deleted}
	case Stopped:
		allowed = []State{Pending, Deleted}
	case Failed:
		allowed = []State{Stopped, Deleted}
	case Deleted:
		return false
	}
	return slices.Contains(allowed, next)
}
