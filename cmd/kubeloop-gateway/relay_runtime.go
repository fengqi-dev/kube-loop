package main

import (
	"errors"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/gateway"
	"github.com/fengqi-dev/kube-loop/internal/gateway/relayagent"
	"github.com/fengqi-dev/kube-loop/internal/gateway/websocketmux"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relaycontrol"
)

func expandRelayEndpoint(template string, environment gatewayEnvironment) (string, error) {
	result := strings.TrimSpace(template)
	for placeholder, metadata := range map[string]struct {
		name  string
		value string
	}{
		"{podName}": {name: "KUBELOOP_POD_NAME", value: environment.PodName},
		"{podUID}":  {name: "KUBELOOP_POD_UID", value: environment.PodUID},
	} {
		if !strings.Contains(result, placeholder) {
			continue
		}
		if metadata.value == "" {
			return "", errors.New(metadata.name + " is required by the Relay endpoint template")
		}
		result = strings.ReplaceAll(result, placeholder, metadata.value)
	}
	if strings.ContainsAny(result, "{}") {
		return "", errors.New("relay endpoint contains an unknown template placeholder")
	}
	return result, nil
}

type relayRuntimeReporter struct {
	gateway         *gateway.Server
	websocket       *websocketmux.Handler
	maximumPhysical uint32
	maximumLogical  uint32
}

func (reporter *relayRuntimeReporter) Snapshot() (relaycontrol.State, relaycontrol.Capacity) {
	state := relaycontrol.StateReady
	if reporter.gateway.Draining() || reporter.websocket.Draining() {
		state = relaycontrol.StateDraining
	}
	return state, relaycontrol.Capacity{
		MaximumPhysicalConnections: reporter.maximumPhysical,
		MaximumLogicalStreams:      reporter.maximumLogical,
		//nolint:gosec // The WebSocket limiter keeps active sessions within the validated uint32 maximum.
		ActivePhysicalConnections: uint32(reporter.websocket.ActiveSessions()),
		//nolint:gosec // The Gateway tracks logical connections within the validated uint32 maximum.
		ActiveLogicalStreams: uint32(reporter.gateway.ActiveConnections()),
	}
}

func (reporter *relayRuntimeReporter) BeginDrain() {
	reporter.gateway.BeginDrain()
	reporter.websocket.BeginDrain()
}

type runtimeGateway interface {
	Draining() bool
	ActiveConnections() int
}

type relayReadiness interface {
	Ready() bool
}

type operationsGatewayState struct {
	gateway runtimeGateway
	agent   relayReadiness
}

func (state operationsGatewayState) Ready() bool {
	if state.gateway == nil || state.gateway.Draining() {
		return false
	}
	return state.agent == nil || state.agent.Ready()
}

func (state operationsGatewayState) Draining() bool {
	return state.gateway != nil && state.gateway.Draining()
}

func (state operationsGatewayState) ActiveConnections() int {
	if state.gateway == nil {
		return 0
	}
	return state.gateway.ActiveConnections()
}

var _ runtimeGateway = (*gateway.Server)(nil)
var _ relayReadiness = (*relayagent.Agent)(nil)
