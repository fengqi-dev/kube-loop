package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func (m *Model) updateErrorPopup(message tea.Msg) (tea.Cmd, bool) {
	switch msg := message.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case keyCtrlC:
			return nil, false
		case "y", "c":
			value := m.err
			m.status = "Copying error..."
			return copySessionToClipboard(m.context(), "error", value), true
		case keyEnter, keyEsc, "q":
			m.err = ""
			return nil, true
		default:
			return nil, true
		}
	case tea.MouseMsg:
		return nil, true
	default:
		return nil, false
	}
}

func (m Model) viewErrorPopup(height int) string {
	width := minInt(88, max(40, m.width-8))
	messageWidth := max(24, width-6)
	message := lipgloss.NewStyle().
		Width(messageWidth).
		Foreground(lipgloss.Color("#FF5F68")).
		Render(strings.TrimSpace(m.err))
	content := consoleError.Bold(true).Render("ERROR") + "\n\n" +
		message + "\n\n" +
		consoleSubtle.Render("y/c copy error   Enter/Esc close")
	box := consoleOverlayBox.Width(width).Render(content)
	return lipgloss.Place(m.width, height, lipgloss.Center, lipgloss.Center, box)
}
