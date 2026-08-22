package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *Model) runWorkspaceCommand(command string) tea.Cmd {
	command = strings.TrimSpace(strings.TrimPrefix(command, ":"))
	if command == "" {
		return nil
	}
	m.workspace.commands = appendUniqueWorkspaceCommand(m.workspace.commands, command)
	fields := strings.Fields(strings.ToLower(command))
	name := fields[0]
	switch name {
	case "connect":
		return m.beginConnect()
	case "disconnect":
		return m.beginDisconnect()
	case "logout":
		return m.beginLogout()
	case "uninstall-service", "uninstall-helper":
		return m.beginServiceUninstall()
	}
	switch name {
	case "q", "quit", "exit":
		return tea.Quit
	case "h", commandHelp, "?":
		m.workspace.help = true
		return nil
	}
	resource, ok := resolveWorkspaceResource(name, m.workspace.config)
	if !ok {
		m.err = "unknown command: " + command
		return nil
	}
	if resource == resourcePods || resource == resourceServices {
		if len(fields) > 1 {
			target := fields[1]
			if target == "all" {
				target = ""
			}
			if target != "" && !containsNamespace(m.namespaces, target) {
				m.err = "unknown namespace: " + fields[1]
				return nil
			}
			if target == m.namespace {
				return m.workspaceNavigate(resource, true)
			}
			m.namespaceReturnResource = resource
			return m.beginNamespaceSwitch(target)
		}
	}
	return m.workspaceNavigate(resource, true)
}
