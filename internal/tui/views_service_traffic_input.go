package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) updateServiceTrafficAction(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	fields := 3
	if m.action.mode == actionPreview {
		fields = 5
	}
	switch msg.String() {
	case keyTab:
		m.action.field = (m.action.field + 1) % fields
		return m, nil
	case keyShiftTab:
		m.action.field = (m.action.field - 1 + fields) % fields
		return m, nil
	case "up":
		m.action.field = (m.action.field - 1 + fields) % fields
		return m, nil
	case keyDown:
		m.action.field = (m.action.field + 1) % fields
		return m, nil
	case keyLeft:
		if m.action.mode == actionPreview && m.action.field == 1 {
			m.toggleActionProtocol()
		} else if m.action.mode != actionPreview && m.action.field == 0 && len(m.action.ports) > 1 {
			m.action.portIndex = (m.action.portIndex - 1 + len(m.action.ports)) % len(m.action.ports)
			m.selectActionPort()
			m.action.localPort = strconv.Itoa(int(m.action.port))
		}
		return m, nil
	case keyRight, " ":
		if m.action.mode == actionPreview && m.action.field == 1 {
			m.toggleActionProtocol()
		} else if m.action.mode != actionPreview && m.action.field == 0 && len(m.action.ports) > 1 {
			m.action.portIndex = (m.action.portIndex + 1) % len(m.action.ports)
			m.selectActionPort()
			m.action.localPort = strconv.Itoa(int(m.action.port))
		}
		return m, nil
	case keyBackspace:
		m.backspaceServiceTrafficField()
		return m, nil
	case keyEnter:
		if err := m.validateServiceTrafficAction(); err != nil {
			m.err = err.Error()
			return m, nil
		}
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, m.startServiceTraffic())
	default:
		if len(msg.Runes) > 0 {
			m.appendServiceTrafficInput(msg.Runes)
		}
	}
	return m, nil
}

func (m *Model) toggleActionProtocol() {
	if strings.EqualFold(m.action.protocol, "udp") {
		m.action.protocol = "tcp"
	} else {
		m.action.protocol = "udp"
	}
}

func (m *Model) backspaceServiceTrafficField() {
	if m.action.mode == actionPreview {
		switch m.action.field {
		case 0:
			m.action.previewName = trimLastRune(m.action.previewName)
		case 2:
			m.action.servicePort = trimLastRune(m.action.servicePort)
		case 3:
			m.action.localHost = trimLastRune(m.action.localHost)
		case 4:
			m.action.localPort = trimLastRune(m.action.localPort)
		}
		return
	}
	switch m.action.field {
	case 1:
		m.action.localHost = trimLastRune(m.action.localHost)
	case 2:
		m.action.localPort = trimLastRune(m.action.localPort)
	}
}

func (m *Model) appendServiceTrafficInput(runes []rune) {
	appendPort := func(target *string) {
		for _, character := range runes {
			if character >= '0' && character <= '9' && len(*target) < 5 {
				*target += string(character)
			}
		}
	}
	if m.action.mode == actionPreview {
		switch m.action.field {
		case 0:
			m.action.previewName += string(runes)
		case 2:
			appendPort(&m.action.servicePort)
		case 3:
			m.action.localHost += string(runes)
		case 4:
			appendPort(&m.action.localPort)
		}
		return
	}
	switch m.action.field {
	case 1:
		m.action.localHost += string(runes)
	case 2:
		appendPort(&m.action.localPort)
	}
}

func (m Model) validateServiceTrafficAction() error {
	if strings.TrimSpace(m.action.localHost) == "" {
		return fmt.Errorf("local host is required")
	}
	if m.action.mode == actionPreview {
		if strings.TrimSpace(m.action.previewName) == "" {
			return fmt.Errorf("preview name is required")
		}
		if _, err := parseActionPort(m.action.servicePort, false); err != nil {
			return fmt.Errorf("service port must be between 1 and 65535")
		}
	}
	if _, err := parseActionPort(m.action.localPort, true); err != nil {
		return fmt.Errorf("local port must be between 0 and 65535")
	}
	return nil
}

func parseActionPort(value string, allowZero bool) (uint16, error) {
	if allowZero && strings.TrimSpace(value) == "" {
		return 0, nil
	}
	parsed, err := strconv.ParseUint(strings.TrimSpace(value), 10, 16)
	if err != nil || (!allowZero && parsed == 0) {
		return 0, fmt.Errorf("port is invalid")
	}
	return uint16(parsed), nil
}
