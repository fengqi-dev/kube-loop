package runtime

import (
	"github.com/fengqi-dev/kube-loop/internal/gateway"
	"github.com/fengqi-dev/kube-loop/internal/gateway/relayagent"
	"github.com/fengqi-dev/kube-loop/internal/gateway/websocketmux"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relaycontrol"
)

type Reporter struct {
	Gateway         *gateway.Server
	WebSocket       *websocketmux.Handler
	MaximumPhysical uint32
	MaximumLogical  uint32
}

func (reporter *Reporter) Snapshot() (relaycontrol.State, relaycontrol.Capacity) {
	state := relaycontrol.StateReady
	if reporter.Gateway.Draining() || reporter.WebSocket.Draining() {
		state = relaycontrol.StateDraining
	}
	return state, relaycontrol.Capacity{
		MaximumPhysicalConnections: reporter.MaximumPhysical,
		MaximumLogicalStreams:      reporter.MaximumLogical,
		//nolint:gosec // The WebSocket limiter keeps active sessions within the validated uint32 maximum.
		ActivePhysicalConnections: uint32(reporter.WebSocket.ActiveSessions()),
		//nolint:gosec // The Gateway tracks logical connections within the validated uint32 maximum.
		ActiveLogicalStreams: uint32(reporter.Gateway.ActiveConnections()),
	}
}

func (reporter *Reporter) BeginDrain() {
	reporter.Gateway.BeginDrain()
	reporter.WebSocket.BeginDrain()
}

type Gateway interface {
	Draining() bool
	ActiveConnections() int
}

type RelayReadiness interface {
	Ready() bool
}

type OperationsState struct {
	Gateway Gateway
	Agent   RelayReadiness
}

func (state OperationsState) Ready() bool {
	if state.Gateway == nil || state.Gateway.Draining() {
		return false
	}
	return state.Agent == nil || state.Agent.Ready()
}

func (state OperationsState) Draining() bool {
	return state.Gateway != nil && state.Gateway.Draining()
}

func (state OperationsState) ActiveConnections() int {
	if state.Gateway == nil {
		return 0
	}
	return state.Gateway.ActiveConnections()
}

var _ Gateway = (*gateway.Server)(nil)
var _ RelayReadiness = (*relayagent.Agent)(nil)
