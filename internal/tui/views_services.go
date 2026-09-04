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
		m.action.mode, m.action.service, m.action.pod = actionPortForward, service.Name, ""
		m.action.ports = make([]actionPortOption, 0, len(service.Ports))
		for _, port := range service.Ports {
			m.action.ports = append(
				m.action.ports,
				actionPortOption{Name: port.Name, Port: port.Port, Protocol: port.Protocol},
			)
		}
		m.action.portIndex, m.action.localPort, m.action.field = 0, "0", 0
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
			m.action.mode = actionExchange
		} else {
			m.action.mode = actionMirror
		}
		m.action.service, m.action.pod = service.Name, ""
		m.action.ports = make([]actionPortOption, 0, len(service.Ports))
		for _, port := range service.Ports {
			m.action.ports = append(
				m.action.ports,
				actionPortOption{Name: port.Name, Port: port.Port, Protocol: port.Protocol},
			)
		}
		m.action.portIndex, m.action.field = 0, 0
		m.action.localHost = "127.0.0.1"
		m.selectActionPort()
		m.action.localPort = strconv.Itoa(int(m.action.port))
		m.err, m.status = "", ""
	case "p":
		m.action.mode, m.action.service, m.action.pod = actionPreview, "", ""
		m.action.previewName, m.action.protocol = "", "tcp"
		m.action.servicePort, m.action.localPort = "", ""
		m.action.localHost, m.action.field = "127.0.0.1", 0
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
