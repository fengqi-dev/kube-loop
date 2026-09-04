package tui

import (
	"fmt"
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
	if m.action.mode != actionNone {
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
