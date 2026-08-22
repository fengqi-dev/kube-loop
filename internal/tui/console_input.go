package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *Model) updateConsoleInput(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case keyEsc:
		if m.console.inputMode == inputFilter {
			m.console.filters[m.activeTab] = ""
			m.setConsoleCursor(0)
		}
		m.console.inputMode, m.console.inputText = inputNone, ""
		return nil
	case keyEnter:
		text, mode := m.console.inputText, m.console.inputMode
		m.console.inputMode, m.console.inputText = inputNone, ""
		if mode == inputCommand {
			return m.runConsoleCommand(text)
		}
		return nil
	case keyBackspace:
		m.console.inputText = trimLastRune(m.console.inputText)
	case keyLeft, keyRight, "up", keyDown, keyTab, keyShiftTab:
		return nil
	default:
		if key.Type == tea.KeyRunes {
			m.console.inputText += string(key.Runes)
		}
	}
	if m.console.inputMode == inputFilter {
		m.console.filters[m.activeTab] = m.console.inputText
		m.setConsoleCursor(0)
	}
	return nil
}

// runConsoleCommand resolves a k9s-style ":" command.
func (m *Model) runConsoleCommand(command string) tea.Cmd {
	command = strings.ToLower(strings.TrimSpace(command))
	switch command {
	case "":
		return nil
	case "q", "quit", "exit":
		return tea.Quit
	case "h", commandHelp, "?":
		m.console.overlay = overlayHelp
		return nil
	case "ns", commandNamespace, "namespaces":
		if m.mode != viewMain {
			m.err = "log in to select a namespace"
			return nil
		}
		if len(m.namespaces) == 0 {
			m.err = "no namespaces loaded"
			return nil
		}
		m.console.overlay, m.console.query, m.console.overlayPos = overlayNamespace, "", 0
		return nil
	case "server", string(resourceProfiles):
		if m.mode != viewMain {
			m.err = "already managing servers"
			return nil
		}
		if m.connected() {
			m.err = "disconnect before changing server"
			return nil
		}
		m.console.overlay = overlayProfiles
		return loadProfiles(m.state)
	case "c", commandConnection, "connection":
		return m.gotoConsoleTab(tabConnection)
	case "w", "po", resourceKindPod, string(resourcePods), "workloads":
		return m.gotoConsoleTab(tabWorkloads)
	case "v", commandService, resourceKindService, string(resourceServices):
		return m.gotoConsoleTab(tabServices)
	case "s", "session", string(resourceTasks), "fw", taskKindForward, "forwards":
		return m.gotoConsoleTab(tabTasks)
	}
	m.err = "unknown command: " + command
	return nil
}

func (m *Model) gotoConsoleTab(next tab) tea.Cmd {
	if m.mode != viewMain || !m.authSession.Authenticated {
		m.err = "log in to open " + tabNames[next]
		return nil
	}
	if next == m.activeTab {
		return nil
	}
	m.selectConsoleTab(next)
	m.loading = true
	return tea.Batch(m.spinner.Tick, m.loadTabData())
}
