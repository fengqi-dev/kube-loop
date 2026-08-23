package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) updateServiceTrafficAction(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	fields := 3
	if m.actionMode == actionPreview {
		fields = 5
	}
	switch msg.String() {
	case keyTab:
		m.actionField = (m.actionField + 1) % fields
		return m, nil
	case keyShiftTab:
		m.actionField = (m.actionField - 1 + fields) % fields
		return m, nil
	case "up":
		m.actionField = (m.actionField - 1 + fields) % fields
		return m, nil
	case keyDown:
		m.actionField = (m.actionField + 1) % fields
		return m, nil
	case keyLeft:
		if m.actionMode == actionPreview && m.actionField == 1 {
			m.toggleActionProtocol()
		} else if m.actionMode != actionPreview && m.actionField == 0 && len(m.actionPorts) > 1 {
			m.actionPortIndex = (m.actionPortIndex - 1 + len(m.actionPorts)) % len(m.actionPorts)
			m.selectActionPort()
			m.actionLocalPort = strconv.Itoa(int(m.actionPort))
		}
		return m, nil
	case keyRight, " ":
		if m.actionMode == actionPreview && m.actionField == 1 {
			m.toggleActionProtocol()
		} else if m.actionMode != actionPreview && m.actionField == 0 && len(m.actionPorts) > 1 {
			m.actionPortIndex = (m.actionPortIndex + 1) % len(m.actionPorts)
			m.selectActionPort()
			m.actionLocalPort = strconv.Itoa(int(m.actionPort))
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
	if strings.EqualFold(m.actionProtocol, "udp") {
		m.actionProtocol = "tcp"
	} else {
		m.actionProtocol = "udp"
	}
}

func (m *Model) backspaceServiceTrafficField() {
	if m.actionMode == actionPreview {
		switch m.actionField {
		case 0:
			m.actionPreviewName = trimLastRune(m.actionPreviewName)
		case 2:
			m.actionServicePort = trimLastRune(m.actionServicePort)
		case 3:
			m.actionLocalHost = trimLastRune(m.actionLocalHost)
		case 4:
			m.actionLocalPort = trimLastRune(m.actionLocalPort)
		}
		return
	}
	switch m.actionField {
	case 1:
		m.actionLocalHost = trimLastRune(m.actionLocalHost)
	case 2:
		m.actionLocalPort = trimLastRune(m.actionLocalPort)
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
	if m.actionMode == actionPreview {
		switch m.actionField {
		case 0:
			m.actionPreviewName += string(runes)
		case 2:
			appendPort(&m.actionServicePort)
		case 3:
			m.actionLocalHost += string(runes)
		case 4:
			appendPort(&m.actionLocalPort)
		}
		return
	}
	switch m.actionField {
	case 1:
		m.actionLocalHost += string(runes)
	case 2:
		appendPort(&m.actionLocalPort)
	}
}

func (m Model) validateServiceTrafficAction() error {
	if strings.TrimSpace(m.actionLocalHost) == "" {
		return fmt.Errorf("local host is required")
	}
	if m.actionMode == actionPreview {
		if strings.TrimSpace(m.actionPreviewName) == "" {
			return fmt.Errorf("preview name is required")
		}
		if _, err := parseActionPort(m.actionServicePort, false); err != nil {
			return fmt.Errorf("service port must be between 1 and 65535")
		}
	}
	if _, err := parseActionPort(m.actionLocalPort, true); err != nil {
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
