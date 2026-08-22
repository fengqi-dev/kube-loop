package tui

import (
	"fmt"
	"math"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
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

func (m Model) consoleTaskRows() []consoleRow {
	filter := m.console.filters[tabTasks]
	rows := make([]consoleRow, 0, m.consoleTaskCount())
	for index, task := range m.portForwards {
		if m.console.taskFilter != taskFilterAll && m.console.taskFilter != taskFilterForward {
			continue
		}
		detail := fmt.Sprintf(
			"State: %s\nNamespace: %s\nKind: %s\nProtocol: %s\nLocal: %s\nRemote: %s:%d",
			task.State,
			task.Namespace,
			task.Kind,
			task.Protocol,
			task.Address,
			task.DialAddress,
			task.RemotePort,
		)
		row := consoleRow{
			title:  task.Name,
			status: "FORWARD",
			kind:   taskKindForward,
			index:  index,
			meta:   task.Address,
			detail: detail,
		}
		if consoleRowMatchesFilter(row, filter) {
			rows = append(rows, row)
		}
	}
	base := len(m.portForwards)
	for index, task := range m.exchanges {
		if m.console.taskFilter == taskFilterAll || m.console.taskFilter == taskFilterTraffic {
			row := trafficConsoleRow(
				"EXCHANGE",
				"exchange",
				base+index,
				task.Service,
				task.Namespace,
				task.ClusterIP,
				task.State,
				task.Targets,
			)
			if consoleRowMatchesFilter(row, filter) {
				rows = append(rows, row)
			}
		}
	}
	base += len(m.exchanges)
	for index, task := range m.mirrors {
		if m.console.taskFilter == taskFilterAll || m.console.taskFilter == taskFilterTraffic {
			row := mirrorConsoleRow(base+index, task)
			if consoleRowMatchesFilter(row, filter) {
				rows = append(rows, row)
			}
		}
	}
	base += len(m.mirrors)
	for index, task := range m.previews {
		if m.console.taskFilter == taskFilterAll || m.console.taskFilter == taskFilterTraffic {
			row := trafficConsoleRow(
				"PREVIEW",
				taskKindPreview,
				base+index,
				task.Name,
				task.Namespace,
				task.ClusterIP,
				task.State,
				task.Targets,
			)
			if consoleRowMatchesFilter(row, filter) {
				rows = append(rows, row)
			}
		}
	}
	base += len(m.previews)
	for index, task := range m.podSSHEndpoints {
		if m.console.taskFilter != taskFilterAll && m.console.taskFilter != taskFilterSSH {
			continue
		}
		detail := "State: " + task.State +
			"\nNamespace: " + task.Namespace +
			"\nPod IP: " + task.PodIP +
			"\nContainer: " + task.Container +
			"\nAddress: " + task.Address +
			"\nCommand: " + task.Command
		row := consoleRow{
			title: task.Pod, status: taskStatusSSH, kind: taskKindSSH, index: base + index,
			meta: task.Command, detail: detail,
		}
		if consoleRowMatchesFilter(row, filter) {
			rows = append(rows, row)
		}
	}
	for index, task := range m.execTasks {
		if m.console.taskFilter != taskFilterAll && m.console.taskFilter != taskFilterExec {
			continue
		}
		detail := "Command: " + task.Command + "\nState: " + task.State
		if task.Output != "" {
			detail += "\n\nOUTPUT\n" + task.Output
		}
		row := consoleRow{
			title: task.Pod, status: taskStatusExec, kind: taskKindExec,
			index: base + len(m.podSSHEndpoints) + index, meta: task.Command, detail: detail,
		}
		if consoleRowMatchesFilter(row, filter) {
			rows = append(rows, row)
		}
	}
	return rows
}

func (m Model) consoleItemCount() int {
	switch m.activeTab {
	case tabWorkloads:
		return len(m.consoleWorkloadRows())
	case tabServices:
		return len(m.consoleServiceRows())
	case tabTasks:
		return len(m.consoleTaskRows())
	case tabConnection, tabCount:
		return 0
	}
	return 0
}

func (m Model) consoleTaskCount() int {
	return len(m.portForwards) + len(m.exchanges) + len(m.mirrors) +
		len(m.previews) + len(m.podSSHEndpoints) + len(m.execTasks)
}

func (m Model) consoleExecIndex(rowIndex int) int {
	return rowIndex - len(m.portForwards) - len(m.exchanges) - len(m.mirrors) -
		len(m.previews) - len(m.podSSHEndpoints)
}

func (m Model) filteredNamespaces() []string {
	query := strings.ToLower(strings.TrimSpace(m.console.query))
	matches := make([]string, 0, len(m.namespaces)+1)
	//nolint:gocritic // The user query is intentionally matched against the literal namespace candidate.
	if query == "" || strings.Contains("all", query) {
		matches = append(matches, "all")
	}
	for _, namespace := range m.namespaces {
		if query == "" || strings.Contains(strings.ToLower(namespace.Name), query) {
			matches = append(matches, namespace.Name)
		}
	}
	return matches
}

func (m Model) consoleTaskTitle() string {
	labels := []string{"ALL", "FORWARD", "TRAFFIC", taskStatusSSH, taskStatusExec}
	return "SESSIONS · " + labels[minInt(m.console.taskFilter, len(labels)-1)]
}

func (m Model) selectedConsoleTask() (consoleRow, bool) {
	rows := m.consoleTaskRows()
	if m.cursor < 0 || m.cursor >= len(rows) {
		return consoleRow{}, false
	}
	return rows[m.cursor], true
}

func (m *Model) clearCompletedExecTasks() {
	kept := m.execTasks[:0]
	removed := 0
	for _, task := range m.execTasks {
		if task.State == taskStateRunning {
			kept = append(kept, task)
		} else {
			removed++
		}
	}
	m.execTasks = kept
	m.status = fmt.Sprintf("Cleared %d completed exec session(s)", removed)
	m.setConsoleCursor(minInt(m.cursor, max(0, m.consoleItemCount()-1)))
}

func (m *Model) copySelectedTaskOutput() tea.Cmd {
	row, ok := m.selectedConsoleTask()
	if !ok {
		m.err = "select a session to copy"
		return nil
	}
	value := firstNonEmpty(row.copy, row.meta)
	if row.kind == taskKindExec {
		index := m.consoleExecIndex(row.index)
		if index >= 0 && index < len(m.execTasks) && m.execTasks[index].Output != "" {
			value = m.execTasks[index].Output
		}
	}
	if strings.TrimSpace(value) == "" {
		value = row.detail
	}
	if strings.TrimSpace(value) == "" {
		m.err = "selected session has no copyable details"
		return nil
	}
	m.err, m.status = "", "Copying "+strings.ToUpper(row.kind)+" session..."
	return copySessionToClipboard(m.context(), row.kind, value)
}

func (m Model) consoleStateMessage(empty string) string {
	if m.loading {
		return m.spinner.View() + " Loading..."
	}
	if m.err != "" {
		return consoleError.Render(m.err) + "\n\n" + consoleSubtle.Render("Press r to retry")
	}
	return consoleSubtle.Render(empty)
}

func (m Model) viewConsoleProfilesOverlay() string {
	lines := []string{consoleSection.Render("MANAGE SERVERS"), ""}
	for index, profile := range m.profiles.Profiles {
		marker, style := "  ", consoleValue
		if index == m.loginCursor {
			marker, style = "> ", consoleSelected
		}
		name := truncateConsole(firstNonEmpty(profile.DisplayName, profile.ID), 20)
		line := marker + name + "  " + truncateConsole(profile.BaseURL, 24)
		lines = append(lines, style.Width(48).Render(line))
	}
	if len(m.profiles.Profiles) == 0 {
		lines = append(lines, consoleSubtle.Render("No servers configured."))
	}
	lines = append(lines, "", consoleSubtle.Render("Enter select   l login   a add   d delete   Esc close"))
	return strings.Join(lines, "\n")
}

func formatPodPorts(ports []clientremote.PodPort) string {
	items := make([]string, 0, len(ports))
	for _, port := range ports {
		name := ""
		if port.Name != "" {
			name = port.Name + ":"
		}
		items = append(items, fmt.Sprintf("%s%d/%s", name, port.Port, port.Protocol))
	}
	return firstNonEmpty(strings.Join(items, ", "), "-")
}

func cropConsoleText(value string, offset, height int) string {
	lines := strings.Split(value, "\n")
	offset = minInt(max(0, offset), max(0, len(lines)-1))
	return strings.Join(lines[offset:minInt(len(lines), offset+height)], "\n")
}

func consoleConfirmContent(title, message, action string) string {
	return consoleError.Render(title) + "\n\n" + message + "\n\n" +
		consoleDangerButton.Render(" Enter / y  "+action) + "  " +
		consoleSubtle.Render("n / Esc cancel")
}

func renderConsoleField(label, value string) string {
	return consoleSubtle.Width(14).Render(label) + consoleValue.Render(value) + "\n\n"
}

func truncateConsole(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(value) <= width {
		return value
	}
	if width <= 3 {
		return strings.Repeat(".", width)
	}
	runes := []rune(value)
	for len(runes) > 0 && lipgloss.Width(string(runes))+3 > width {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "..."
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" && value != "<nil>" {
			return value
		}
	}
	return ""
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
