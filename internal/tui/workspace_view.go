package tui

import (
	"fmt"

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
