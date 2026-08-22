package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m Model) viewWorkspace() string {
	if !m.workspace.initialized {
		return m.viewConsole()
	}
	if m.width < workspaceMinWidth || m.height < workspaceMinHeight {
		return m.viewWorkspaceTooSmall()
	}
	if m.workspace.help && m.err == "" && !m.connectionProgressActive() {
		return m.viewWorkspaceHelp()
	}
	header := m.viewWorkspaceHeader()
	commandBar := m.viewWorkspaceCommandBar()
	top := header
	if commandBar != "" {
		top = lipgloss.JoinVertical(lipgloss.Left, header, commandBar)
	}
	footer := m.viewWorkspaceFooter()
	bodyHeight := max(5, m.height-lipgloss.Height(top)-lipgloss.Height(footer))
	if m.err != "" && m.workspace.input == workspaceInputNone {
		body := m.viewErrorPopup(bodyHeight)
		return lipgloss.JoinVertical(lipgloss.Left, top, body, footer)
	}
	if m.connectionProgressActive() {
		body := m.viewConnectionProgressPopup(bodyHeight)
		return lipgloss.JoinVertical(lipgloss.Left, top, body, footer)
	}
	if m.loginAdding {
		progress := ""
		if m.loading {
			progress = "\n\n" + m.spinner.View() + " Discovering server…"
		}
		form := consoleSection.Render("ADD SERVER") + "\n\n" +
			consoleSubtle.Render("Enter the complete HTTP or HTTPS Gateway service address.") + "\n\n" +
			"Service address\n> " + m.loginURL + "_\n\n" +
			"Enter add server   Esc cancel" + progress
		body := lipgloss.Place(
			m.width,
			bodyHeight,
			lipgloss.Center,
			lipgloss.Center,
			consoleOverlayBox.Width(minInt(72, m.width-8)).Render(form),
		)
		return lipgloss.JoinVertical(lipgloss.Left, top, body, footer)
	}
	if m.actionMode != actionNone {
		body := m.viewConsoleAction(m.width, bodyHeight)
		return lipgloss.JoinVertical(lipgloss.Left, top, body, footer)
	}
	if m.console.overlay != overlayNone {
		body := m.viewConsoleOverlay(m.width, bodyHeight)
		return lipgloss.JoinVertical(lipgloss.Left, top, body, footer)
	}
	body := m.viewWorkspaceBody(bodyHeight)
	return lipgloss.JoinVertical(lipgloss.Left, top, body, footer)
}

func (m Model) viewWorkspaceTooSmall() string {
	size := fmt.Sprintf(
		"Current: %dx%d  Required: %dx%d",
		m.width,
		m.height,
		workspaceMinWidth,
		workspaceMinHeight,
	)
	body := consoleSection.Render("KUBELOOP") +
		"\n\nTerminal is too small.\n" + size +
		"\n\nResize the terminal or press q to quit."
	box := consoleOverlayBox.Width(max(40, m.width-8)).Render(body)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m Model) viewWorkspaceHeader() string {
	server := truncateConsole(firstNonEmpty(m.activeProfile.BaseURL, "-"), 25)
	user := truncateConsole(firstNonEmpty(m.authSession.UserName, "-"), 25)
	namespace := truncateConsole(firstNonEmpty(m.namespace, "all"), 25)
	field := func(name, text string) string {
		return consoleSection.Render(name+":") + " " + consoleValue.Bold(true).Render(text)
	}
	left := strings.Join([]string{
		field("Cluster", server),
		field("User", user),
		field("Namespace", namespace),
		field("Mode", strings.ToUpper(string(m.selectedMode))),
		field("KubeLoop Rev", workspaceBuildRevision(m.version)),
		field("K8s Rev", "n/a"),
	}, "\n")

	keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#21B8FF")).Bold(true)
	namespaceKeyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FF00D7")).Bold(true)
	shortcut := func(key, text string) string {
		style := keyStyle
		if key == "0" || key == "1" {
			style = namespaceKeyStyle
		}
		return style.Render("<"+key+">") + " " + consoleSubtle.Render(text)
	}
	leftWidth := minInt(34, max(28, m.width/4))
	logoWidth := 0
	if m.width >= 120 {
		logoWidth = 28
	}
	shortcutWidth := max(1, m.width-leftWidth-logoWidth)
	shortcutColumnWidth := max(22, shortcutWidth/3)
	shortcutRow := func(first, second, third string) string {
		column := lipgloss.NewStyle().Width(shortcutColumnWidth).MaxWidth(shortcutColumnWidth)
		row := column.Render(first) + column.Render(second)
		if third != "" {
			row += column.Render(third)
		}
		return row
	}
	var shortcuts string
	if m.workspace.resource == resourceConnection {
		connectionAction := "Connect"
		if m.connected() {
			connectionAction = "Disconnect"
		}
		shortcuts = strings.Join([]string{
			shortcutRow(shortcut("a", "Add Server"), shortcut("?", "Help"), shortcut("r", "Refresh")),
			shortcutRow(shortcut("c", connectionAction), shortcut("q", "Quit"), shortcut("-", "Back")),
			shortcutRow(shortcut("p", "Pods"), shortcut("v", "Services"), shortcut("n", "Namespaces")),
			shortcutRow(shortcut("s", "Sessions"), shortcut(":", "Command"), shortcut("m", "Mode")),
			shortcutRow(shortcut("enter", "Toggle"), shortcut("]", "Forward"), shortcut("esc", "Cancel")),
			shortcutRow(shortcut("L", "Logout"), shortcut("u", "Uninstall Service"), ""),
		}, "\n")
	} else {
		rows := [][3]string{}
		namespaceRows := func() {
			rows = append(
				rows,
				[3]string{shortcut("0", "all"), shortcut("enter", "Describe"), ""},
				[3]string{
					shortcut("1", firstNonEmpty(m.activeProfile.LastNamespace, "default")),
					shortcut("n", "Namespace"),
					"",
				},
			)
		}
		switch m.workspace.resource {
		case resourcePods:
			namespaceRows()
			rows = append(rows,
				[3]string{shortcut("f", "Port-Forward"), shortcut("s", "SSH"), ""},
			)
		case resourceServices:
			namespaceRows()
			rows = append(rows,
				[3]string{shortcut("f", "Port-Forward"), shortcut("x", "Exchange"), shortcut("m", "Mirror")},
				[3]string{shortcut("p", "Preview"), "", ""},
			)
		case resourceTasks:
			rows = append(rows,
				[3]string{shortcut("enter", "Describe"), shortcut("d", "Stop"), shortcut("y", "Copy")},
				[3]string{shortcut("e", "Rerun"), shortcut("C", "Clear"), ""},
			)
		case resourceProfiles:
			rows = append(rows,
				[3]string{shortcut("enter", "Select"), shortcut("a", "Add"), shortcut("l", "Login")},
				[3]string{shortcut("d", "Delete"), shortcut("L", "Logout"), ""},
			)
		case resourceNamespaces:
			rows = append(rows, [3]string{shortcut("enter", "Select"), shortcut("/", "Filter"), ""})
		case resourceConnection:
			// Connection shortcuts are added before the resource-specific switch.
		}
		if m.workspace.resource != resourceNamespaces {
			rows = append(rows, [3]string{shortcut(":", "Command"), shortcut("/", "Filter"), shortcut("r", "Refresh")})
		} else {
			rows = append(rows, [3]string{shortcut(":", "Command"), shortcut("r", "Refresh"), ""})
		}
		rows = append(rows,
			[3]string{shortcut("?", "Help"), shortcut("q", "Quit"), shortcut("esc", "Cancel")},
			[3]string{shortcut("-", "Back"), shortcut("]", "Forward"), ""},
		)
		rendered := make([]string, 0, len(rows))
		for _, row := range rows {
			rendered = append(rendered, shortcutRow(row[0], row[1], row[2]))
		}
		shortcuts = strings.Join(rendered, "\n")
	}

	if m.width < 96 {
		compact := field("NS", namespace)
		keys := shortcut(":", "Cmd")
		switch m.workspace.resource {
		case resourceConnection:
			keys = shortcut("c", "Connect") + "  " + shortcut("m", "Mode")
		case resourcePods:
			keys = shortcut("f", "Forward") + "  " + shortcut("s", "SSH") + "  " + keys
		case resourceServices:
			keys = shortcut("f", "Forward") + "  " + shortcut("x", "Exchange") + "  " + keys
		case resourceTasks:
			keys = shortcut("d", "Stop") + "  " + shortcut("y", "Copy") + "  " + keys
		case resourceProfiles:
			keys = shortcut("a", "Add") + "  " + shortcut("l", "Login") + "  " + keys
		case resourceNamespaces:
			keys = shortcut("enter", "Select") + "  " + keys
		}
		return lipgloss.JoinVertical(lipgloss.Left, compact, keys)
	}

	if m.width < 120 {
		return lipgloss.JoinHorizontal(lipgloss.Top,
			lipgloss.NewStyle().Width(leftWidth).Render(left),
			lipgloss.NewStyle().Width(max(1, m.width-leftWidth)).Render(shortcuts),
		)
	}
	logo := consoleSection.Render(" _  __ _   _ ____  _____\n" +
		"| |/ /| | | | __ )| ____|\n" +
		"| ' / | | | |  _ \\|  _|\n" +
		"| . \\ | |_| | |_) | |___\n" +
		"|_|\\_\\ \\___/|____/|_____|\n" +
		"        KUBELOOP\n")
	middleWidth := max(42, m.width-leftWidth-logoWidth)
	return lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.NewStyle().Width(leftWidth).Render(left),
		lipgloss.NewStyle().Width(middleWidth).Render(shortcuts),
		lipgloss.NewStyle().Width(logoWidth).Render(logo),
	)
}

func workspaceBuildRevision(version string) string {
	version = strings.TrimSpace(version)
	if version == "" || version == "(devel)" {
		return "dev"
	}
	return truncateConsole(version, 20)
}

func (m Model) viewWorkspaceCommandBar() string {
	if m.workspace.input != workspaceInputCommand && m.workspace.input != workspaceInputFilter {
		return ""
	}
	prompt := consoleSection.Render("🐶>")
	input := m.workspace.inputText
	if m.workspace.input == workspaceInputFilter {
		input = "/" + input
	}
	line := prompt + " " + consoleCmdText.Render(input+"█")
	return lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(consoleTeal).
		Padding(0, 1).
		Width(max(24, m.width-4)).
		Render(line)
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
