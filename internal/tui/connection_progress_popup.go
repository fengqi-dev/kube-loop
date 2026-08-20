package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func (m Model) connectionProgressActive() bool {
	_, _, _, ok := m.connectionProgress()
	return ok
}

func (m Model) connectionProgress() (int, int, string, bool) {
	if !m.loading || !strings.HasPrefix(m.status, "[") {
		return 0, 0, "", false
	}
	end := strings.IndexByte(m.status, ']')
	if end < 0 {
		return 0, 0, "", false
	}
	step, total := 0, 0
	if _, err := fmt.Sscanf(m.status[1:end], "%d/%d", &step, &total); err != nil || step < 1 || total < step {
		return 0, 0, "", false
	}
	label := strings.TrimSpace(strings.TrimSuffix(m.status[end+1:], "..."))
	return step, total, label, true
}

func (m *Model) updateConnectionProgressPopup(message tea.Msg) (tea.Cmd, bool) {
	switch msg := message.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return nil, false
		}
		return nil, true
	case tea.MouseMsg:
		return nil, true
	default:
		return nil, false
	}
}

func (m Model) viewConnectionProgressPopup(height int) string {
	step, total, label, _ := m.connectionProgress()
	const barWidth = 36
	completed := barWidth * step / total
	bar := consoleOK.Render(strings.Repeat("=", completed)) +
		consoleSubtle.Render(strings.Repeat("-", barWidth-completed))
	content := consoleSection.Render("CONNECTING") + "\n\n" +
		m.spinner.View() + " " + consoleValue.Copy().Bold(true).Render(label) + "\n\n" +
		bar + "  " + consoleOK.Render(fmt.Sprintf("%d/%d", step, total)) + "\n\n" +
		consoleSubtle.Render("Please wait. The dialog closes automatically.")
	width := minInt(68, max(44, m.width-8))
	box := consoleOverlayBox.Copy().Width(width).Render(content)
	return lipgloss.Place(m.width, height, lipgloss.Center, lipgloss.Center, box)
}
