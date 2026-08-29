package tui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

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
		right = "p pause  r resume  d delete  y copy  / filter  : command  ? help"
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
			right = "p pause  r resume  d delete  y copy  / : ?"
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
