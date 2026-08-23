package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

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
