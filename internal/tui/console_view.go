package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m Model) viewConsole() string {
	if m.width == 0 || m.height == 0 {
		return "Loading KubeLoop..."
	}
	if m.width < consoleMinWidth || m.height < consoleMinHeight {
		return m.viewConsoleMinimum()
	}
	if m.mode == viewLogin {
		return m.viewConsoleProfiles()
	}

	header, footer := m.viewConsoleHeader(), m.viewConsoleFooter()
	bodyHeight := max(8, m.height-lipgloss.Height(header)-lipgloss.Height(footer)-2)
	contentWidth := m.width - 2
	var body string
	if m.width >= consoleWideWidth {
		body = lipgloss.JoinHorizontal(
			lipgloss.Top,
			m.viewConsoleSidebar(bodyHeight),
			" ",
			m.viewConsoleMain(m.width-25, bodyHeight),
		)
	} else {
		nav := m.viewConsoleTopNav()
		body = lipgloss.JoinVertical(
			lipgloss.Left,
			nav,
			m.viewConsoleMain(contentWidth, bodyHeight-lipgloss.Height(nav)-1),
		)
	}
	if m.actionMode != actionNone {
		body = m.viewConsoleAction(contentWidth, bodyHeight)
	}
	if m.console.overlay != overlayNone {
		body = m.viewConsoleOverlay(contentWidth, bodyHeight)
	}
	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

func (m Model) viewConsoleMinimum() string {
	size := fmt.Sprintf(
		"Current: %dx%d  Required: %dx%d",
		m.width,
		m.height,
		consoleMinWidth,
		consoleMinHeight,
	)
	box := consoleOverlayBox.Width(max(30, m.width-4)).Render(
		consoleSection.Render("KUBELOOP OPERATIONS CONSOLE") + "\n\n" +
			"Terminal is too small.\n" + size +
			"\n\nResize the terminal or press q to quit.",
	)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m Model) viewConsoleHeader() string {
	server := firstNonEmpty(m.activeProfile.DisplayName, m.activeProfile.BaseURL, "No server")
	state := consoleSubtle.Render("OFFLINE")
	if m.connected() {
		state = consoleOK.Render("ONLINE")
	}
	left := consoleTitle.Render("KUBELOOP") + "  " + consoleSection.Render("OPERATIONS CONSOLE")
	right := truncateConsole(server, 28) + "  " + state
	gap := strings.Repeat(" ", max(1, m.width-lipgloss.Width(left)-lipgloss.Width(right)-2))
	return lipgloss.NewStyle().Width(m.width).Background(consolePanel).Padding(1, 1).Render(left + gap + right)
}

func (m Model) viewConsoleFooter() string {
	right := "[ list  ] details   y copy   ? help   q quit"
	var left string
	switch m.console.inputMode {
	case inputCommand:
		left = consoleCmdPrompt.Render(":") + consoleCmdText.Render(m.console.inputText+"█")
		right = "Enter run   Esc cancel"
	case inputFilter:
		left = consoleFilterPrompt.Render("/") + consoleCmdText.Render(m.console.inputText+"█")
		right = "Enter keep   Esc clear"
	case inputNone:
		left = m.hintText()
		switch {
		case m.loading:
			left = m.spinner.View() + " Working"
		case m.err != "":
			left = consoleError.Render(truncateConsole(m.err, max(20, m.width-30)))
		case m.status != "":
			left = consoleOK.Render(truncateConsole(m.status, max(20, m.width-30)))
		}
	}
	gap := strings.Repeat(" ", max(1, m.width-lipgloss.Width(left)-lipgloss.Width(right)-2))
	return lipgloss.NewStyle().
		Width(m.width).
		Foreground(consoleDim).
		Background(consolePanel).
		Padding(0, 1).
		Render(left + gap + right)
}

func (m Model) viewConsoleSidebar(height int) string {
	items := make([]string, 0, 2+2*len(tabNames)+5)
	items = append(items, consoleSection.Render("NAVIGATION"), "")
	for i, name := range tabNames {
		style, marker := consoleNav, "  "
		if tab(i) == m.activeTab {
			style, marker = consoleNavActive, "> "
		}
		items = append(items, style.Width(18).Render(marker+name), "")
	}
	items = append(
		items,
		"",
		consoleSubtle.Render("Namespace"),
		truncateConsole(m.namespace, 18),
		"",
		consoleSubtle.Render("Press : for commands"),
	)
	return consoleCard.Width(20).Height(max(8, height-2)).Render(strings.Join(items, "\n"))
}

func (m Model) viewConsoleTopNav() string {
	cell := max(12, (m.width-2)/int(tabCount))
	items := make([]string, 0, int(tabCount))
	for i, name := range tabNames {
		style := consoleNav
		if tab(i) == m.activeTab {
			style = consoleNavActive
		}
		items = append(items, style.Width(cell-2).Align(lipgloss.Center).Render(name))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, items...)
}

func (m Model) viewConsoleMain(width, height int) string {
	switch m.activeTab {
	case tabConnection:
		return m.viewConsoleConnection(width)
	case tabWorkloads:
		return m.viewConsoleRows(
			"WORKLOADS",
			m.consoleWorkloadRows(),
			width,
			height,
			"No workloads found in this namespace.",
		)
	case tabServices:
		return m.viewConsoleRows(
			"SERVICES",
			m.consoleServiceRows(),
			width,
			height,
			"No services found in this namespace.",
		)
	case tabTasks:
		return m.viewConsoleRows(m.consoleTaskTitle(), m.consoleTaskRows(), width, height, "No matching sessions.")
	case tabCount:
		return ""
	}
	return ""
}

func (m Model) viewConsoleConnection(width int) string {
	state := "Disconnected"
	connecting := m.loading && strings.HasPrefix(m.status, "[")
	if connecting {
		state = "Connecting"
	}
	if m.connected() {
		state = consoleStateConnected
	}
	leftFields := renderConsoleField("State", state) +
		renderConsoleField(
			"Server",
			firstNonEmpty(m.activeProfile.DisplayName, m.activeProfile.BaseURL, "Not selected"),
		) +
		renderConsoleField("Namespace", firstNonEmpty(m.namespace, "Not selected")) +
		renderConsoleField("Mode", strings.ToUpper(string(m.selectedMode)))
	left := consoleCard.Width(max(24, width/2-3)).Render(
		consoleSection.Render("CONNECTION") + "\n\n" +
			leftFields,
	)
	right := consoleDetail.Width(max(24, width/2-3)).Render(
		consoleSection.Render("QUICK ACTIONS") +
			"\n\n" + consoleButton.Render(" Enter  Connect / Disconnect ") +
			"\n\n" +
			"m  Switch data-plane mode\n" + "n  Select namespace\n" + ":servers  Manage servers\n" + "L  Log out",
	)
	if width >= 76 {
		return lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
	}
	return lipgloss.JoinVertical(lipgloss.Left, left, right)
}

func (m Model) viewConsoleRows(title string, rows []consoleRow, width, height int, empty string) string {
	if len(rows) == 0 {
		message := empty
		if filter := strings.TrimSpace(m.console.filters[m.activeTab]); filter != "" {
			message = "No matches for /" + filter + ". Press Esc to clear the filter."
		} else if !m.connected() && m.activeTab != tabTasks {
			message = "Connect to a server to load this view."
		}
		content := consoleSection.Render(title) + "\n\n" + m.consoleStateMessage(message)
		return consoleCard.
			Width(max(24, width-4)).
			Height(max(6, height-3)).
			Render(content)
	}
	state := &m.console.views[m.activeTab]
	state.cursor = minInt(len(rows)-1, max(0, state.cursor))
	m.cursor = state.cursor
	listWidth, detailWidth := width, 0
	if width >= 84 {
		listWidth, detailWidth = width*3/5-1, width-(width*3/5-1)-1
	}
	visible := max(3, height-6)
	if state.cursor < state.offset {
		state.offset = state.cursor
	}
	if state.cursor >= state.offset+visible {
		state.offset = state.cursor - visible + 1
	}
	end := minInt(len(rows), state.offset+visible)
	heading := fmt.Sprintf("%s  %d", title, len(rows))
	if filter := strings.TrimSpace(m.console.filters[m.activeTab]); filter != "" {
		heading += consoleSubtle.Render(fmt.Sprintf("  /%s", filter))
	}
	lines := []string{consoleSection.Render(heading), ""}
	for i := state.offset; i < end; i++ {
		row, marker, style := rows[i], "  ", consoleValue
		if i == state.cursor {
			marker, style = "> ", consoleSelected
		}
		line := marker + truncateConsole(row.title, max(12, listWidth-24))
		if row.status != "" {
			line += "  " + truncateConsole(row.status, 12)
		}
		lines = append(lines, style.Width(max(20, listWidth-6)).Render(line))
		if row.meta != "" && listWidth >= 52 {
			lines = append(lines, consoleSubtle.Render("    "+truncateConsole(row.meta, listWidth-10)))
		}
	}
	listStyle := consoleCard
	if state.focus == focusList {
		listStyle = listStyle.BorderForeground(consoleTeal)
	}
	list := listStyle.
		Width(max(24, listWidth-4)).
		Height(max(6, height-3)).
		Render(strings.Join(lines, "\n"))
	if detailWidth == 0 {
		return list
	}
	selected := rows[state.cursor]
	detailStyle := consoleDetail
	if state.focus == focusDetail {
		detailStyle = detailStyle.BorderForeground(consoleTeal)
	}
	detailBody := cropConsoleText(
		firstNonEmpty(selected.detail, "No additional details."),
		state.detailOffset,
		max(3, height-9),
	)
	detailContent := consoleSection.Render("DETAILS") + "\n\n" +
		consoleValue.Render(selected.title) + "\n" +
		consoleSubtle.Render(selected.meta) + "\n\n" +
		lipgloss.NewStyle().Width(max(18, detailWidth-8)).Render(detailBody)
	detail := detailStyle.
		Width(max(22, detailWidth-4)).
		Height(max(6, height-3)).
		Render(detailContent)
	return lipgloss.JoinHorizontal(lipgloss.Top, list, " ", detail)
}

func (m Model) viewConsoleAction(width, height int) string {
	title, description := "PORT FORWARD", "Configure the target, then press Enter to start."
	if m.actionMode == actionExec {
		title, description = "EXECUTE COMMAND", "Type the command to run in the selected pod."
	}
	if m.actionMode == actionExchange || m.actionMode == actionMirror || m.actionMode == actionPreview {
		return m.viewServiceTrafficAction(width, height)
	}
	values := "Target: " + firstNonEmpty(m.actionPod, m.actionService, "-") +
		"\nPort: " + strconv.Itoa(int(m.actionPort)) +
		"\nCommand: " + m.actionCommand
	controls := ""
	if m.actionMode == actionPortForward {
		portName := ""
		if m.actionPortIndex >= 0 && m.actionPortIndex < len(m.actionPorts) &&
			m.actionPorts[m.actionPortIndex].Name != "" {
			portName = m.actionPorts[m.actionPortIndex].Name + "  "
		}
		remoteValue := fmt.Sprintf(
			"%s%d/%s",
			portName,
			m.actionPort,
			strings.ToUpper(firstNonEmpty(m.actionProtocol, "tcp")),
		)
		localValue := firstNonEmpty(m.actionLocalPort, "0")
		if localValue == "0" {
			localValue += " (auto)"
		}
		remotePrefix, localPrefix := "  ", "  "
		if m.actionField == 0 {
			remotePrefix = "> "
		} else {
			localPrefix = "> "
			localValue += "_"
		}
		values = "Target: " + firstNonEmpty(m.actionPod, m.actionService, "-") + "\n\n" +
			remotePrefix + "Remote port  " + remoteValue + "\n" +
			localPrefix + "Local port   " + localValue
		controls = "\n\n" + consoleSubtle.Render("↑/↓ field   ←/→ select port   Tab next   0 = auto")
	}
	content := consoleSection.Render(title) + "\n\n" +
		description + "\n\n" +
		consoleValue.Render(values) + controls + "\n\n" +
		consoleButton.Render(" Enter  Start ") + "  Esc cancel"
	box := consoleOverlayBox.Width(minInt(68, width-8)).Render(content)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

func (m Model) viewConsoleOverlay(width, height int) string {
	var content string
	switch m.console.overlay {
	case overlayHelp:
		const commandHelpText = `
:pods :svc :sessions :conn   Go to view
:ns      Select namespace
:servers Manage servers
:help    This reference
:q       Quit

`
		const keyHelpText = `
/                 Filter current list
Esc               Cancel / clear filter
Tab / Shift+Tab   Change view
j / k             Move or scroll
PgUp / PgDn       Move one page
[ / ]             Focus list / details
Enter             Primary action
n                 Select namespace
t                 Filter sessions
y                 Copy session output
r                 Refresh
?                 Close help
q                 Quit`
		content = consoleSection.Render("KEYBOARD REFERENCE") + "\n\n" +
			consoleSection.Render("Commands (press :)") + commandHelpText +
			consoleSection.Render("Keys") + keyHelpText
	case overlayNamespace:
		matches := m.filteredNamespaces()
		lines := []string{
			consoleSection.Render("SELECT NAMESPACE"),
			"",
			"Search: " + consoleValue.Render(m.console.query+"_"),
			"",
		}
		start, end := max(0, m.console.overlayPos-4), minInt(len(matches), max(0, m.console.overlayPos-4)+9)
		for i := start; i < end; i++ {
			line := "  " + matches[i]
			if i == m.console.overlayPos {
				line = consoleSelected.Width(42).Render("> " + matches[i])
			}
			lines = append(lines, line)
		}
		if len(matches) == 0 {
			lines = append(lines, consoleSubtle.Render("No matching namespaces"))
		}
		lines = append(lines, "", consoleSubtle.Render("Type to filter   Enter select   Esc cancel"))
		content = strings.Join(lines, "\n")
	case overlayConfirmTask:
		content = consoleConfirmContent("STOP SESSION?", "The selected client session will be stopped.", "Stop session")
	case overlayConfirmProfile:
		content = consoleConfirmContent("DELETE SERVER?", "The selected server will be removed.", "Delete server")
	case overlayConfirmDisconnect:
		content = consoleConfirmContent(
			"DISCONNECT?",
			fmt.Sprintf("%d active session(s) will be interrupted.", m.consoleTaskCount()),
			"Disconnect",
		)
	case overlayConfirmServiceUninstall:
		content = consoleConfirmContent(
			"UNINSTALL HELPER SERVICE?",
			"The privileged system Helper Service will be removed.",
			"Uninstall service",
		)
	case overlayProfiles:
		content = m.viewConsoleProfilesOverlay()
	case overlayProfileAdd:
		content = consoleSection.Render("ADD SERVER") + "\n\n" +
			consoleSubtle.Render("Enter the complete HTTP or HTTPS Gateway service address.") + "\n\n" +
			"Service address\n> " + m.loginURL + "_\n\n" +
			"Enter add server   Esc back"
	case overlayNone:
		content = ""
	}
	box := consoleOverlayBox.Width(minInt(62, width-8)).Render(content)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

func (m Model) viewConsoleProfiles() string {
	headerContent := consoleTitle.Render("KUBELOOP") + "  " + consoleSection.Render("SERVERS")
	header := lipgloss.NewStyle().
		Width(m.width).
		Background(consolePanel).
		Padding(1, 1).
		Render(headerContent)
	footerLeft := consoleSubtle.Render("Enter select   a add   d delete   l login   : cmd   ? help")
	if m.console.inputMode == inputCommand {
		footerLeft = consoleCmdPrompt.Render(":") +
			consoleCmdText.Render(m.console.inputText+"█") + "   " +
			consoleSubtle.Render("Enter run   Esc cancel")
	}
	footer := lipgloss.NewStyle().
		Width(m.width).
		Foreground(consoleDim).
		Background(consolePanel).
		Padding(0, 1).
		Render(footerLeft)
	height := max(8, m.height-lipgloss.Height(header)-lipgloss.Height(footer)-2)
	var body string
	if m.loginAdding {
		form := consoleSection.Render("ADD SERVER") + "\n\n" +
			consoleSubtle.Render("Enter the complete HTTP or HTTPS Gateway service address.") + "\n\n" +
			"Service address\n> " + m.loginURL + "_\n\n" +
			"Enter add server   Esc cancel"
		body = lipgloss.Place(
			m.width,
			height,
			lipgloss.Center,
			lipgloss.Center,
			consoleOverlayBox.Width(minInt(72, m.width-8)).Render(form),
		)
	} else {
		lines := []string{consoleSection.Render(fmt.Sprintf("SERVERS  %d", len(m.profiles.Profiles))), ""}
		for i, profile := range m.profiles.Profiles {
			marker, style := "  ", consoleValue
			if i == m.loginCursor {
				marker, style = "> ", consoleSelected
			}
			name := firstNonEmpty(profile.DisplayName, profile.ID, "Unnamed server")
			line := marker + truncateConsole(name, max(18, m.width/2)) + "  " +
				truncateConsole(profile.BaseURL, max(18, m.width/2-8))
			lines = append(lines, style.Width(max(30, m.width-10)).Render(line))
		}
		if len(m.profiles.Profiles) == 0 {
			lines = append(lines, consoleSubtle.Render("No servers. Press a to add one."))
		}
		body = consoleCard.
			Width(m.width - 6).
			Height(max(6, height-3)).
			Render(strings.Join(lines, "\n"))
	}
	if m.console.overlay != overlayNone {
		body = m.viewConsoleOverlay(m.width-2, height)
	}
	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}
