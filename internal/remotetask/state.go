// Package remotetask defines the shared lifecycle contract for every durable
// V2 operation exposed by the Gateway.
package remotetask

import (
	"errors"
	"fmt"
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
)

var orderedStates = []State{Pending, Starting, Running, Recovering, Failed, Stopping, Stopped}

func (state State) Valid() bool {
	switch state {
	case Pending, Starting, Running, Recovering, Failed, Stopping, Stopped:
		return true
	default:
		return false
	}
}

func (state State) Terminal() bool {
	return state == Failed || state == Stopped
}

func (state State) Owned() bool {
	switch state {
	case Starting, Running, Recovering, Stopping:
		return true
	default:
		return false
	}
}

func States() []State {
	return append([]State(nil), orderedStates...)
}

func ValidateTransition(current, next State) error {
	if !current.Valid() || !next.Valid() {
		return errors.New("remote Task transition contains an invalid state")
	}
	if current == next {
		if current.Terminal() || current == Pending {
			return fmt.Errorf("remote Task state %q cannot heartbeat", current)
		}
		return nil
	}
	allowed := false
	switch current {
	case Pending:
		allowed = next == Starting || next == Running || next == Stopping || next == Stopped || next == Failed
	case Starting:
		allowed = next == Running || next == Stopping || next == Stopped || next == Failed || next == Recovering
	case Running:
		allowed = next == Stopping || next == Stopped || next == Failed || next == Recovering
	case Stopping:
		allowed = next == Stopped || next == Failed || next == Recovering
	case Recovering:
		allowed = next == Stopping || next == Stopped || next == Failed
	}
	if !allowed {
		return fmt.Errorf("remote Task transition %q -> %q is not allowed", current, next)
	}
	return nil
}
