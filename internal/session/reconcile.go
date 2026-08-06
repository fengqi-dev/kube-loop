package session

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/cluster"
	"github.com/fengqi-dev/kube-loop/internal/intercept"
	"github.com/fengqi-dev/kube-loop/internal/portfwd"
)

const maxActivityEvents = 200

// LogEvent is a structured activity log entry shown on the Logs page.
type LogEvent struct {
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
}

func (m *Manager) appendLogLocked(level, message string) {
	event := LogEvent{Time: time.Now(), Level: level, Message: message}
	m.stateHub.state.Events = append(m.stateHub.state.Events, event)
	if len(m.stateHub.state.Events) > maxActivityEvents {
		m.stateHub.state.Events = append(
			[]LogEvent{},
			m.stateHub.state.Events[len(m.stateHub.state.Events)-maxActivityEvents:]...,
		)
	}
	log.Printf("%s: %s", level, message)
}

func (m *Manager) AppendLog(level, message string) {
	m.stateHub.mu.Lock()
	m.appendLogLocked(level, message)
	next := m.stateHub.state
	m.stateHub.mu.Unlock()
	m.publish(next)
}

// recordLog stores an event without immediately republishing the full session
// state. Lifecycle code uses it when a synchronous subscriber could otherwise
// block teardown; the next state transition carries the event to subscribers.
func (m *Manager) recordLog(level, message string) {
	m.stateHub.mu.Lock()
	m.appendLogLocked(level, message)
	m.stateHub.mu.Unlock()
}

func (m *Manager) reconcileBindings(ctx context.Context, snap cluster.InventorySnapshot) {
	pods := indexPods(snap.PodItems)
	services := indexServices(snap.ServiceItems)

	for _, item := range m.portfwd.List() {
		reason := stalePortForwardReason(item, pods, services)
		if reason == "" {
			continue
		}
		if err := m.portfwd.Stop(item.ID); err != nil {
			m.AppendLog("ERROR", fmt.Sprintf(
				"stop port-forward %s/%s/%s: %v", item.Kind, item.Namespace, item.Name, err,
			))
			continue
		}
		m.persistPortForwards()
		m.AppendLog("INFO", fmt.Sprintf(
			"stopped port-forward %s/%s/%s:%d (%s)",
			item.Kind, item.Namespace, item.Name, item.RemotePort, reason,
		))
	}

	for _, item := range m.intercept.List() {
		reason := staleServiceBindingReason(item.Namespace, item.Service, item.Locals, services)
		if reason == "" {
			continue
		}
		if err := m.intercept.Stop(ctx, item.ID); err != nil {
			m.AppendLog("ERROR", fmt.Sprintf("stop exchange %s/%s: %v", item.Namespace, item.Service, err))
			continue
		}
		if !m.isRestoring() {
			m.persistExchanges(m.State().Context)
		}
		m.AppendLog("INFO", fmt.Sprintf("stopped exchange %s/%s (%s)", item.Namespace, item.Service, reason))
	}

	for _, item := range m.intercept.ListMirrors() {
		reason := staleServiceBindingReason(item.Namespace, item.Service, item.Locals, services)
		if reason == "" {
			continue
		}
		if err := m.intercept.Stop(ctx, item.ID); err != nil {
			m.AppendLog("ERROR", fmt.Sprintf("stop mirror %s/%s: %v", item.Namespace, item.Service, err))
			continue
		}
		if !m.isRestoring() {
			m.persistMirrors(m.State().Context)
		}
		m.AppendLog("INFO", fmt.Sprintf("stopped mirror %s/%s (%s)", item.Namespace, item.Service, reason))
	}

	for _, item := range m.intercept.ListPreviews() {
		reason := staleServiceBindingReason(item.Namespace, item.Service, item.Locals, services)
		if reason == "" {
			continue
		}
		if err := m.intercept.Stop(ctx, item.ID); err != nil {
			m.AppendLog("ERROR", fmt.Sprintf("stop preview %s/%s: %v", item.Namespace, item.Service, err))
			continue
		}
		if !m.isRestoring() {
			m.persistPreviews(m.State().Context)
		}
		m.AppendLog("INFO", fmt.Sprintf("stopped preview %s/%s (%s)", item.Namespace, item.Service, reason))
	}
}

func stalePortForwardReason(
	item portfwd.Info,
	pods map[string]cluster.PodInfo,
	services map[string]cluster.ServiceInfo,
) string {
	key := item.Namespace + "/" + item.Name
	switch item.Kind {
	case portfwd.KindPod:
		pod, ok := pods[key]
		if !ok {
			return "pod deleted"
		}
		if !cluster.PodHasPort(pod, item.RemotePort) {
			return "pod port removed"
		}
	case portfwd.KindService:
		service, ok := services[key]
		if !ok {
			return "service deleted"
		}
		if !cluster.ServiceHasPort(service, int32(item.RemotePort)) {
			return "service port removed"
		}
	}
	return ""
}

func staleServiceBindingReason(
	namespace, service string,
	locals []intercept.PortMapping,
	services map[string]cluster.ServiceInfo,
) string {
	key := namespace + "/" + service
	info, ok := services[key]
	if !ok {
		return "service deleted"
	}
	for _, local := range locals {
		if !cluster.ServiceHasPort(info, local.ServicePort) {
			return "service port removed"
		}
	}
	return ""
}

func indexPods(items []cluster.PodInfo) map[string]cluster.PodInfo {
	out := make(map[string]cluster.PodInfo, len(items))
	for _, item := range items {
		out[item.Namespace+"/"+item.Name] = item
	}
	return out
}

func indexServices(items []cluster.ServiceInfo) map[string]cluster.ServiceInfo {
	out := make(map[string]cluster.ServiceInfo, len(items))
	for _, item := range items {
		out[item.Namespace+"/"+item.Name] = item
	}
	return out
}
