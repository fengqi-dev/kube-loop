package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func workspacePadLine(value string, width int) string {
	value = truncateConsole(value, width)
	return value + strings.Repeat(" ", max(0, width-lipgloss.Width(value)))
}

func (m Model) workspaceTableHeader() string {
	wide := m.width >= 100
	switch m.workspace.resource {
	case resourcePods:
		if wide {
			nsW, nameW, pfW, readyW, statusW, restartW, addrW, nodeW, ageW := m.workspacePodColumnWidths()
			return fmt.Sprintf(
				"%-*s %-*s %-*s %-*s %-*s %-*s %-*s %-*s %-*s",
				nsW, "NAMESPACE",
				nameW, "NAME",
				pfW, "PF",
				readyW, "READY",
				statusW, "STATUS",
				restartW, "RESTARTS",
				addrW, "POD IP",
				nodeW, "NODE",
				ageW, "AGE",
			)
		}
		return fmt.Sprintf("%-16s %-24s %-7s %s", "NAMESPACE", "NAME", "READY", "STATUS")
	case resourceServices:
		if wide {
			nsW, nameW, kindW, clusterW, externalW, portsW, ageW := m.workspaceServiceColumnWidths()
			return fmt.Sprintf(
				"%-*s %-*s %-*s %-*s %-*s %-*s %-*s",
				nsW, "NAMESPACE",
				nameW, "NAME",
				kindW, "TYPE",
				clusterW, "CLUSTER-IP",
				externalW, "EXTERNAL-IP",
				portsW, "PORTS",
				ageW, "AGE",
			)
		}
		return fmt.Sprintf("%-16s %-24s %-14s %s", "NAMESPACE", "NAME", "TYPE", "PORTS")
	case resourceTasks:
		return consoleSubtle.Render(
			fmt.Sprintf("  %-10s %-30s %-36s %s", "TYPE", "TARGET", "COMMAND / ADDRESS", "STATE"),
		)
	case resourceProfiles:
		return consoleSubtle.Render(fmt.Sprintf("  %-24s %-52s %s", "NAME", "ENDPOINT", "STATUS"))
	case resourceNamespaces:
		return consoleSubtle.Render(fmt.Sprintf("  %-48s %s", "NAME", "STATUS"))
	case resourceConnection:
		return ""
	}
	return ""
}

func (m Model) workspaceTableRow(row consoleRow) string {
	wide := m.width >= 100
	switch m.workspace.resource {
	case resourcePods:
		ready := workspaceMetaValue(row.meta, "ready")
		node := workspaceMetaValue(row.meta, "node")
		namespace := workspaceDetailValue(row.detail, "Namespace")
		ip := workspaceDetailValue(row.detail, "Pod IP")
		if wide {
			nsW, nameW, pfW, readyW, statusW, restartW, addrW, nodeW, ageW := m.workspacePodColumnWidths()
			return fmt.Sprintf(
				"%-*s %-*s %-*s %-*s %-*s %-*s %-*s %-*s %-*s",
				nsW, truncateConsole(namespace, nsW),
				nameW, truncateConsole(row.title, nameW),
				pfW, m.workspacePodForwardMark(namespace, row.title),
				readyW, truncateConsole(ready, readyW),
				statusW, truncateConsole(row.status, statusW),
				restartW, workspaceMetaValue(row.meta, "restarts"),
				addrW, truncateConsole(ip, addrW),
				nodeW, truncateConsole(node, nodeW),
				ageW, workspaceMetaValue(row.meta, "age"),
			)
		}
		return fmt.Sprintf(
			"%-16s %-24s %-7s %s",
			truncateConsole(namespace, 16),
			truncateConsole(row.title, 24),
			ready,
			truncateConsole(row.status, 12),
		)
	case resourceServices:
		namespace := workspaceDetailValue(row.detail, "Namespace")
		ports := strings.TrimPrefix(row.meta, "ports ")
		if wide {
			nsW, nameW, kindW, clusterW, externalW, portsW, ageW := m.workspaceServiceColumnWidths()
			return fmt.Sprintf(
				"%-*s %-*s %-*s %-*s %-*s %-*s %-*s",
				nsW, truncateConsole(namespace, nsW),
				nameW, truncateConsole(row.title, nameW),
				kindW, truncateConsole(row.status, kindW),
				clusterW, truncateConsole(workspaceDetailValue(row.detail, "Cluster IP"), clusterW),
				externalW, truncateConsole(workspaceDetailValue(row.detail, "External IP"), externalW),
				portsW, truncateConsole(ports, portsW),
				ageW, workspaceDetailValue(row.detail, "Age"),
			)
		}
		return fmt.Sprintf(
			"%-16s %-24s %-14s %s",
			truncateConsole(namespace, 16),
			truncateConsole(row.title, 24),
			truncateConsole(row.status, 14),
			ports,
		)
	case resourceTasks:
		return fmt.Sprintf(
			"%-10s %-30s %-36s %s",
			row.status,
			truncateConsole(row.title, 30),
			truncateConsole(row.meta, 36),
			workspaceDetailValue(row.detail, "State"),
		)
	case resourceProfiles:
		return fmt.Sprintf("%-24s %-52s %s", truncateConsole(row.title, 24), truncateConsole(row.meta, 52), row.status)
	case resourceNamespaces:
		return fmt.Sprintf("%-48s %s", truncateConsole(row.title, 48), row.status)
	case resourceConnection:
		return row.title
	}
	return row.title
}

func (m Model) workspacePodColumnWidths() (namespace, name, pf, ready, status, restarts, address, node, age int) {
	width := max(98, m.width-2)
	namespace = m.workspaceNamespaceColumnWidth()
	pf, ready, status, restarts, address, node, age = 2, 5, 10, 8, 14, 12, 5
	name = max(12, width-namespace-pf-ready-status-restarts-address-node-age-8)
	return namespace, name, pf, ready, status, restarts, address, node, age
}

func (m Model) workspaceServiceColumnWidths() (namespace, name, kind, clusterIP, externalIP, ports, age int) {
	width := max(98, m.width-2)
	namespace = m.workspaceNamespaceColumnWidth()
	kind, clusterIP, externalIP, age = 11, 15, 15, 5
	remaining := max(26, width-namespace-kind-clusterIP-externalIP-age-6)
	name = max(18, remaining*55/100)
	ports = max(8, remaining-name)
	return namespace, name, kind, clusterIP, externalIP, ports, age
}

func (m Model) workspaceNamespaceColumnWidth() int {
	width := lipgloss.Width("NAMESPACE")
	switch m.workspace.resource {
	case resourcePods:
		for _, pod := range m.pods {
			width = max(width, lipgloss.Width(pod.Namespace))
		}
	case resourceServices:
		for _, service := range m.services {
			width = max(width, lipgloss.Width(service.Namespace))
		}
	case resourceConnection, resourceTasks, resourceProfiles, resourceNamespaces:
		// These resources do not render a namespace column.
	}
	return minInt(22, max(14, width+1))
}

func (m Model) workspacePodForwardMark(_ string, _ string) string {
	return "●"
}

func formatResourceAge(seconds int64) string {
	switch {
	case seconds <= 0:
		return "-"
	case seconds >= 86400:
		return fmt.Sprintf("%dd", seconds/86400)
	case seconds >= 3600:
		return fmt.Sprintf("%dh", seconds/3600)
	case seconds >= 60:
		return fmt.Sprintf("%dm", seconds/60)
	default:
		return fmt.Sprintf("%ds", max(0, int(seconds)))
	}
}

func workspaceMetaValue(meta, key string) string {
	fields := strings.Fields(meta)
	for index := range fields {
		if fields[index] == key && index+1 < len(fields) {
			return fields[index+1]
		}
	}
	return "-"
}

func workspaceDetailValue(detail, key string) string {
	prefix := key + ":"
	for line := range strings.SplitSeq(detail, "\n") {
		if after, ok := strings.CutPrefix(line, prefix); ok {
			return strings.TrimSpace(after)
		}
	}
	return "-"
}

func (m Model) viewWorkspaceBody(height int) string {
	if m.workspace.resource == resourceConnection {
		return m.viewWorkspaceConnection(height)
	}
	view := m.workspaceView()
	if view.detail {
		return m.viewWorkspaceDetail(height)
	}
	rows := m.workspaceFilteredRows()
	filter := view.filter
	heading := workspaceDescriptor(m.workspace.resource).title
	if m.workspace.resource == resourcePods || m.workspace.resource == resourceServices {
		heading += "(" + firstNonEmpty(m.namespace, "all") + ")"
	}
	heading += fmt.Sprintf("[%d]", len(rows))
	if filter != "" {
		heading += "   Filter: /" + filter
	}
	innerWidth := max(30, m.width-2)
	heading = truncateConsole(heading, max(10, innerWidth-4))
	titleWidth := lipgloss.Width(heading)
	leftRule := max(0, (innerWidth-titleWidth-2)/2)
	rightRule := max(0, innerWidth-titleWidth-leftRule-2)
	border := lipgloss.NewStyle().Foreground(consoleTeal).Background(lipgloss.Color("#000000"))
	rowStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#8BD5FF")).Background(lipgloss.Color("#000000"))
	headerStyle := rowStyle.Bold(true)
	selectedStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#071018")).
		Background(lipgloss.Color("#7CC9F2")).
		Bold(true)
	top := border.Render("┌"+strings.Repeat("─", leftRule)+" ") +
		consoleOK.Render(heading) +
		border.Render(" "+strings.Repeat("─", rightRule)+"┐")
	header := border.Render("│") +
		headerStyle.Render(workspacePadLine(m.workspaceTableHeader(), innerWidth)) +
		border.Render("│")
	lines := []string{top, header}
	page := max(1, height-3)
	if view.cursor < view.offset {
		view.offset = view.cursor
	}
	if view.cursor >= view.offset+page {
		view.offset = view.cursor - page + 1
	}
	end := minInt(len(rows), view.offset+page)
	for index := view.offset; index < end; index++ {
		line := workspacePadLine(m.workspaceTableRow(rows[index]), innerWidth)
		if index == view.cursor {
			line = selectedStyle.Render(line)
		} else {
			line = rowStyle.Render(line)
		}
		lines = append(lines, border.Render("│")+line+border.Render("│"))
	}
	if len(rows) == 0 {
		message := "No resources found."
		if filter != "" {
			message = "No matches for /" + filter
		}
		emptyRow := border.Render("│") +
			rowStyle.Render(workspacePadLine(" "+message, innerWidth)) +
			border.Render("│")
		lines = append(lines, emptyRow)
	}
	for len(lines) < height-1 {
		lines = append(lines, border.Render("│")+rowStyle.Render(strings.Repeat(" ", innerWidth))+border.Render("│"))
	}
	if len(lines) > height-1 {
		lines = lines[:height-1]
	}
	lines = append(lines, border.Render("└"+strings.Repeat("─", innerWidth)+"┘"))
	return strings.Join(lines, "\n")
}
