package tui

import (
	"fmt"
	"strconv"
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

func (m Model) viewWorkspaceDetail(height int) string {
	rows := m.workspaceFilteredRows()
	view := m.workspaceView()
	if len(rows) == 0 || view.cursor >= len(rows) {
		return consoleSubtle.Render("No selected resource.")
	}
	row := rows[view.cursor]
	descriptor := workspaceDescriptor(m.workspace.resource)
	breadcrumb := string(m.workspace.resource) + " > " + row.title
	innerWidth := max(30, m.width-8)
	bodyHeight := max(5, height-8)
	body := cropConsoleText(firstNonEmpty(row.detail, "No additional details."), 0, bodyHeight-2)
	details := consoleCard.Width(innerWidth).Height(bodyHeight).Render(
		consoleSection.Render("DETAILS") + "\n\n" + consoleValue.Render(body),
	)
	actionText := descriptor.actions
	if m.workspace.resource == resourceServices {
		actionText = strings.ReplaceAll(actionText, "  p preview", "")
	}
	actions := consoleDetail.Width(innerWidth).Render(
		consoleSection.Render("AVAILABLE ACTIONS") + "\n" + actionText,
	)
	title := consoleSection.Render(descriptor.title+" / "+row.title) + "\n" + consoleSubtle.Render(row.meta)
	return lipgloss.JoinVertical(lipgloss.Left, title, details, actions, consoleSubtle.Render(breadcrumb))
}

func (m Model) viewWorkspaceConnection(height int) string {
	state := "Disconnected"
	connecting := m.loading && strings.HasPrefix(m.status, "[")
	if connecting {
		state = "Connecting"
	}
	if m.connected() {
		state = consoleStateConnected
	}
	server := firstNonEmpty(m.activeProfile.DisplayName, m.activeProfile.BaseURL, "Not selected")
	endpoint := firstNonEmpty(m.activeProfile.BaseURL, "-")
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#21B8FF")).Bold(true).Width(16)
	valueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#8BD5FF")).Bold(true)
	field := func(label, value string) string {
		return labelStyle.Render(label) + valueStyle.Render(value) + "\n\n"
	}
	stateStyle := consoleError
	if connecting {
		stateStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFB454"))
	}
	if m.connected() {
		stateStyle = consoleOK
	}
	leftBody := field("SERVER", server) +
		field("ENDPOINT", endpoint) +
		field("USER", firstNonEmpty(m.authSession.UserName, "-"))
	rightBody := labelStyle.Render("STATE") + stateStyle.Bold(true).Render(state) + "\n\n" +
		field("MODE", strings.ToUpper(string(m.selectedMode))) +
		field("SESSIONS", strconv.Itoa(m.consoleTaskCount()))
	panelHeight := max(8, height-3)
	panel := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#8BD5FF")).
		Background(lipgloss.Color("#000000")).
		Border(lipgloss.NormalBorder()).
		BorderForeground(consoleTeal).
		Padding(1, 2).
		Width(max(30, m.width-4)).
		Height(panelHeight)
	if m.width >= 82 {
		columnWidth := max(26, m.width/2-7)
		left := lipgloss.NewStyle().Width(columnWidth).Render(leftBody)
		right := lipgloss.NewStyle().Width(columnWidth).Render(rightBody)
		body := lipgloss.JoinHorizontal(lipgloss.Top, left, "  ", right)
		return panel.Render(consoleOK.Render("connection") + "\n\n" + body)
	}
	body := leftBody + "\n" + rightBody
	return panel.Render(consoleOK.Render("connection") + "\n\n" + body)
}

func (m Model) viewWorkspaceFooter() string {
	left := "<" + string(m.workspace.resource) + ">"
	inputHints := "  : command  ? help"
	if m.workspace.resource != resourceConnection {
		inputHints = "  / filter" + inputHints
	}
	right := workspaceDescriptor(m.workspace.resource).actions + inputHints
	if m.workspace.resource == resourceTasks {
		right = "d stop  y copy  / filter  : command  ? help"
	}
	lines := []string{}
	if m.workspace.warning != "" {
		lines = append(lines, consoleError.Render("Config: "+truncateConsole(m.workspace.warning, m.width-10)))
	}
	switch {
	case m.loading:
		left = m.spinner.View() + " Working"
	case m.err != "" && m.workspace.input != workspaceInputNone:
		left = consoleError.Render(truncateConsole(m.err, max(20, m.width-20)))
	case m.status != "":
		left = consoleOK.Render(truncateConsole(m.status, max(20, m.width-20)))
	}
	switch m.workspace.input {
	case workspaceInputCommand:
		left = consoleSection.Render("COMMAND")
		right = "Tab complete   ↑/↓ history   Enter run   Esc cancel"
	case workspaceInputFilter:
		left = consoleSection.Render("FILTER")
		right = "RE2   ! inverse   -f fuzzy   Enter keep   Esc cancel"
	case workspaceInputNone:
		// Default footer content is already set above.
	}
	if m.width < 86 {
		shortcuts := "  : ?"
		if m.workspace.resource != resourceConnection {
			shortcuts = "  / : ?"
		}
		right = workspaceDescriptor(m.workspace.resource).actions + shortcuts
		if m.workspace.resource == resourceTasks {
			right = "d stop  y copy  / : ?"
		}
	}
	gap := strings.Repeat(" ", max(1, m.width-lipgloss.Width(left)-lipgloss.Width(right)-2))
	footer := lipgloss.NewStyle().
		Width(m.width).
		Foreground(consoleDim).
		Background(consolePanel).
		Padding(0, 1).
		Render(left + gap + right)
	lines = append(lines, footer)
	return strings.Join(lines, "\n")
}

func (m Model) viewWorkspaceHelp() string {
	content := consoleSection.Render("K9S WORKSPACE HELP") + "\n\n" +
		": command     / filter       ? help        q back/quit\n" +
		"j/k move      g/G first/last  Ctrl+u/d page  Enter inspect\n" +
		"- or [ back   ] forward       r refresh\n\n" +
		"RESOURCES\n  :connection  :pods  :services  :sessions  :servers  :namespaces/:ns\n\n" +
		"ACTIONS\n  :connect  :disconnect  :logout  :uninstall-service\n\n" +
		"FILTERS\n  /pattern RE2   /!pattern inverse   /-f text fuzzy\n\n" +
		"CURRENT ACTIONS\n  " + workspaceDescriptor(m.workspace.resource).actions
	box := consoleOverlayBox.Width(minInt(82, m.width-8)).Render(content)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}
