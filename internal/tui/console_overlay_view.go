package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

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
