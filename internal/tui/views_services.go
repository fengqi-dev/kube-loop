package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
)

func (m Model) updateServices(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case keyDown, "j":
		if m.cursor < len(m.services)-1 {
			m.cursor++
		}
	case "n":
		return m, m.cycleNamespace()
	case "f", keyEnter:
		row, ok := m.selectedConsoleRow()
		if !ok || row.index >= len(m.services) {
			return m, nil
		}
		service := m.services[row.index]
		if len(service.Ports) == 0 {
			m.err = "service exposes no ports"
			return m, nil
		}
		m.actionMode, m.actionService, m.actionPod = actionPortForward, service.Name, ""
		m.actionPorts = make([]actionPortOption, 0, len(service.Ports))
		for _, port := range service.Ports {
			m.actionPorts = append(
				m.actionPorts,
				actionPortOption{Name: port.Name, Port: port.Port, Protocol: port.Protocol},
			)
		}
		m.actionPortIndex, m.actionLocalPort, m.actionField = 0, "0", 0
		m.selectActionPort()
		m.err, m.status = "", ""
	case "x", "m":
		row, ok := m.selectedConsoleRow()
		if !ok || row.index >= len(m.services) {
			return m, nil
		}
		service := m.services[row.index]
		if len(service.Ports) == 0 {
			m.err = "service exposes no ports"
			return m, nil
		}
		if msg.String() == "x" {
			m.actionMode = actionExchange
		} else {
			m.actionMode = actionMirror
		}
		m.actionService, m.actionPod = service.Name, ""
		m.actionPorts = make([]actionPortOption, 0, len(service.Ports))
		for _, port := range service.Ports {
			m.actionPorts = append(
				m.actionPorts,
				actionPortOption{Name: port.Name, Port: port.Port, Protocol: port.Protocol},
			)
		}
		m.actionPortIndex, m.actionField = 0, 0
		m.actionLocalHost = "127.0.0.1"
		m.selectActionPort()
		m.actionLocalPort = strconv.Itoa(int(m.actionPort))
		m.err, m.status = "", ""
	case "p":
		m.actionMode, m.actionService, m.actionPod = actionPreview, "", ""
		m.actionPreviewName, m.actionProtocol = "", "tcp"
		m.actionServicePort, m.actionLocalPort = "", ""
		m.actionLocalHost, m.actionField = "127.0.0.1", 0
		m.err, m.status = "", ""
	}
	return m, nil
}

func formatServicePorts(ports []clientremote.ServicePort) string {
	parts := make([]string, 0, len(ports))
	for _, port := range ports {
		name := ""
		if port.Name != "" {
			name = port.Name + ":"
		}
		target := firstNonEmpty(port.TargetPort, "0")
		parts = append(parts, fmt.Sprintf("%s%d►%s", name, port.Port, target))
	}
	if len(parts) > 4 {
		parts = append(parts[:4], "...")
	}
	return strings.Join(parts, ", ")
}
