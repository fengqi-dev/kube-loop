package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

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
	if m.login.adding {
		form := consoleSection.Render("ADD SERVER") + "\n\n" +
			consoleSubtle.Render("Enter the complete HTTP or HTTPS Gateway service address.") + "\n\n" +
			"Service address\n> " + m.login.url + "_\n\n" +
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
			if i == m.login.cursor {
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
