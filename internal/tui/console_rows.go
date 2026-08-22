package tui

import (
	"fmt"
	"math"
	"strings"
)

func (m Model) consoleWorkloadRows() []consoleRow {
	filter := m.console.filters[tabWorkloads]
	rows := make([]consoleRow, 0, len(m.pods))
	for index, pod := range m.pods {
		name := firstNonEmpty(pod.Name, "Unnamed pod")
		phase := firstNonEmpty(pod.Phase, "Unknown")
		node := firstNonEmpty(pod.NodeName, "-")
		totalContainers := pod.TotalContainers
		if totalContainers == 0 {
			totalContainers = containerCount(pod.Containers)
		}
		readyContainers := pod.ReadyContainers
		if readyContainers == 0 && pod.Ready {
			readyContainers = totalContainers
		}
		ready := fmt.Sprintf("%d/%d", readyContainers, totalContainers)
		containers := firstNonEmpty(strings.Join(pod.Containers, ", "), "-")
		ports := formatPodPorts(pod.Ports)
		meta := fmt.Sprintf(
			"node %s   ready %s   restarts %d   age %s",
			node,
			ready,
			pod.Restarts,
			formatResourceAge(pod.AgeSeconds),
		)
		detail := "Namespace: " + firstNonEmpty(pod.Namespace, m.namespace) +
			"\nPhase: " + phase +
			"\nPod IP: " + firstNonEmpty(pod.PodIP, "-") +
			"\nNode: " + node +
			"\nReady: " + ready +
			fmt.Sprintf("\nRestarts: %d\nAge: %s", pod.Restarts, formatResourceAge(pod.AgeSeconds)) +
			"\nContainers: " + containers +
			"\nPorts: " + ports +
			"\n\nEnter/f port forward   s SSH   e exec"
		row := consoleRow{title: name, status: phase, meta: meta, index: index, detail: detail}
		if consoleRowMatchesFilter(row, filter) {
			rows = append(rows, row)
		}
	}
	return rows
}

func containerCount(containers []string) int32 {
	if len(containers) > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(len(containers)) //nolint:gosec // Length is checked against MaxInt32 above.
}

func (m Model) consoleServiceRows() []consoleRow {
	filter := m.console.filters[tabServices]
	rows := make([]consoleRow, 0, len(m.services))
	for index, service := range m.services {
		name := firstNonEmpty(service.Name, "Unnamed service")
		typeName := firstNonEmpty(service.Type, "Service")
		ports := firstNonEmpty(formatServicePorts(service.Ports), "-")
		ip := firstNonEmpty(service.ClusterIP, "-")
		externalIP := firstNonEmpty(strings.Join(service.ExternalIPs, ","), "<none>")
		detail := "Namespace: " + firstNonEmpty(service.Namespace, m.namespace) +
			"\nType: " + typeName +
			"\nCluster IP: " + ip +
			"\nExternal IP: " + externalIP +
			"\nAge: " + formatResourceAge(service.AgeSeconds) +
			"\nPorts: " + ports +
			"\n\nEnter/f start port forward"
		row := consoleRow{
			title: name, status: typeName, meta: "ports " + ports, index: index, detail: detail,
		}
		if consoleRowMatchesFilter(row, filter) {
			rows = append(rows, row)
		}
	}
	return rows
}
