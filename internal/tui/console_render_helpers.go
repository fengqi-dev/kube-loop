package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
)

func (m Model) consoleStateMessage(empty string) string {
	if m.loading {
		return m.spinner.View() + " Loading..."
	}
	if m.err != "" {
		return consoleError.Render(m.err) + "\n\n" + consoleSubtle.Render("Press r to retry")
	}
	return consoleSubtle.Render(empty)
}

func (m Model) viewConsoleProfilesOverlay() string {
	lines := []string{consoleSection.Render("MANAGE SERVERS"), ""}
	for index, profile := range m.profiles.Profiles {
		marker, style := "  ", consoleValue
		if index == m.login.cursor {
			marker, style = "> ", consoleSelected
		}
		name := truncateConsole(firstNonEmpty(profile.DisplayName, profile.ID), 20)
		line := marker + name + "  " + truncateConsole(profile.BaseURL, 24)
		lines = append(lines, style.Width(48).Render(line))
	}
	if len(m.profiles.Profiles) == 0 {
		lines = append(lines, consoleSubtle.Render("No servers configured."))
	}
	lines = append(lines, "", consoleSubtle.Render("Enter select   l login   a add   d delete   Esc close"))
	return strings.Join(lines, "\n")
}

func formatPodPorts(ports []clientremote.PodPort) string {
	items := make([]string, 0, len(ports))
	for _, port := range ports {
		name := ""
		if port.Name != "" {
			name = port.Name + ":"
		}
		items = append(items, fmt.Sprintf("%s%d/%s", name, port.Port, port.Protocol))
	}
	return firstNonEmpty(strings.Join(items, ", "), "-")
}

func cropConsoleText(value string, offset, height int) string {
	lines := strings.Split(value, "\n")
	offset = minInt(max(0, offset), max(0, len(lines)-1))
	return strings.Join(lines[offset:minInt(len(lines), offset+height)], "\n")
}

func consoleConfirmContent(title, message, action string) string {
	return consoleError.Render(title) + "\n\n" + message + "\n\n" +
		consoleDangerButton.Render(" Enter / y  "+action) + "  " +
		consoleSubtle.Render("n / Esc cancel")
}

func renderConsoleField(label, value string) string {
	return consoleSubtle.Width(14).Render(label) + consoleValue.Render(value) + "\n\n"
}

func truncateConsole(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(value) <= width {
		return value
	}
	if width <= 3 {
		return strings.Repeat(".", width)
	}
	runes := []rune(value)
	for len(runes) > 0 && lipgloss.Width(string(runes))+3 > width {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "..."
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" && value != "<nil>" {
			return value
		}
	}
	return ""
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
