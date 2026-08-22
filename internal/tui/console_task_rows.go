package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

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
