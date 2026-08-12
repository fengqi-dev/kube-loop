package main

import (
	"errors"
	"os"
	"strings"
	"sync"

	"github.com/fengqi-dev/kube-loop/internal/gateway"
	"github.com/fengqi-dev/kube-loop/internal/gateway/relayagent"
	"github.com/fengqi-dev/kube-loop/internal/gateway/websocketmux"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relaycontrol"
)

func expandRelayEndpoint(template string) (string, error) {
	result := strings.TrimSpace(template)
	for placeholder, environment := range map[string]string{
		"{podName}": "KUBELOOP_POD_NAME",
		"{podUID}":  "KUBELOOP_POD_UID",
	} {
		if !strings.Contains(result, placeholder) {
			continue
		}
		value := strings.TrimSpace(os.Getenv(environment))
		if value == "" {
			return "", errors.New(environment + " is required by the Relay endpoint template")
		}
		result = strings.ReplaceAll(result, placeholder, value)
	}
	if strings.ContainsAny(result, "{}") {
		return "", errors.New("Relay endpoint contains an unknown template placeholder")
	}
	return result, nil
}

type relayRuntimeReporter struct {
	gateway         *gateway.Server
	websocket       *websocketmux.Handler
	maximumPhysical uint32
	maximumLogical  uint32
	mu              sync.RWMutex
	traffic         trafficRuntime
}

func (reporter *relayRuntimeReporter) Snapshot() (relaycontrol.State, relaycontrol.Capacity) {
	state := relaycontrol.StateReady
	if reporter.gateway.Draining() || reporter.websocket.Draining() {
		state = relaycontrol.StateDraining
	}
	reporter.mu.RLock()
	traffic := reporter.traffic
	reporter.mu.RUnlock()
	trafficSessions := 0
	if traffic != nil {
		trafficSessions = traffic.ActiveSessions()
		if traffic.Draining() {
			state = relaycontrol.StateDraining
		}
	}
	return state, relaycontrol.Capacity{
		MaximumPhysicalConnections: reporter.maximumPhysical,
		MaximumLogicalStreams:      reporter.maximumLogical,
		ActivePhysicalConnections:  uint32(reporter.websocket.ActiveSessions() + trafficSessions),
		ActiveLogicalStreams:       uint32(reporter.gateway.ActiveConnections()),
	}
}

func (reporter *relayRuntimeReporter) BeginDrain() {
	reporter.gateway.BeginDrain()
	reporter.websocket.BeginDrain()
	reporter.mu.RLock()
	traffic := reporter.traffic
	reporter.mu.RUnlock()
	if traffic != nil {
		traffic.BeginDrain()
	}
}

func (reporter *relayRuntimeReporter) SetTraffic(traffic trafficRuntime) {
	reporter.mu.Lock()
	reporter.traffic = traffic
	reporter.mu.Unlock()
}

type trafficRuntime interface {
	ActiveSessions() int
	Draining() bool
	BeginDrain()
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
