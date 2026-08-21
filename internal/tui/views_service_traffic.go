package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	clientexchange "github.com/fengqi-dev/kube-loop/internal/client/exchange"
	clientmirror "github.com/fengqi-dev/kube-loop/internal/client/mirror"
	clientpreview "github.com/fengqi-dev/kube-loop/internal/client/preview"
	clientreverserelay "github.com/fengqi-dev/kube-loop/internal/client/reverserelay"
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

func (m Model) startServiceTraffic() tea.Cmd {
	return func() tea.Msg {
		profile, ok := m.state.ActiveProfile()
		if !ok {
			return trafficOperationStartedMsg{err: fmt.Errorf("no active server")}
		}
		session, err := m.state.sessions.Current(profile.ID)
		if err != nil {
			return trafficOperationStartedMsg{err: err}
		}
		localPort, _ := parseActionPort(m.actionLocalPort, true)
		localHost := strings.TrimSpace(m.actionLocalHost)
		switch m.actionMode {
		case actionExchange:
			request := clientexchange.Request{
				ProfileID: profile.ID,
				Service:   m.actionService,
				Targets: []clientexchange.LocalTarget{{
					ServicePort: m.actionPort,
					Protocol:    m.actionProtocol,
					LocalHost:   localHost,
					LocalPort:   localPort,
				}},
			}
			_, err = m.state.exchanges.Start(m.state.ctx, profile, session, request)
			return trafficOperationStartedMsg{kind: "Exchange", target: m.actionService, err: err}
		case actionMirror:
			request := clientmirror.Request{
				ProfileID: profile.ID,
				Service:   m.actionService,
				Targets: []clientmirror.LocalTarget{{
					ServicePort: m.actionPort,
					Protocol:    m.actionProtocol,
					LocalHost:   localHost,
					LocalPort:   localPort,
				}},
			}
			_, err = m.state.mirrors.Start(m.state.ctx, profile, session, request)
			return trafficOperationStartedMsg{kind: "Mirror", target: m.actionService, err: err}
		case actionPreview:
			servicePort, _ := parseActionPort(m.actionServicePort, false)
			name := strings.TrimSpace(m.actionPreviewName)
			request := clientpreview.Request{
				ProfileID: profile.ID,
				Namespace: session.Namespace,
				Name:      name,
				Targets: []clientpreview.LocalTarget{{
					ServicePort: int32(servicePort),
					Protocol:    m.actionProtocol,
					LocalHost:   localHost,
					LocalPort:   localPort,
				}},
			}
			_, err = m.state.previews.Start(m.state.ctx, profile, session, request)
			return trafficOperationStartedMsg{kind: "Preview", target: name, err: err}
		case actionNone, actionPortForward, actionExec:
			return trafficOperationStartedMsg{err: fmt.Errorf("traffic operation is invalid")}
		}
		return trafficOperationStartedMsg{err: fmt.Errorf("traffic operation is invalid")}
	}
}

func loadTrafficOperations(state *State, profileID string) tea.Cmd {
	return func() tea.Msg {
		return trafficOperationsLoadedMsg{
			exchanges: state.exchanges.List(profileID),
			mirrors:   state.mirrors.List(profileID),
			previews:  state.previews.List(profileID),
		}
	}
}

func (m Model) viewServiceTrafficAction(width, height int) string {
	title := "EXCHANGE"
	description := "Configure the Service port and local target, then press Enter to start."
	if m.actionMode == actionMirror {
		title = "MIRROR"
	}
	if m.actionMode == actionPreview {
		title = "CREATE PREVIEW"
		description = "Configure the Preview Service and local target, then press Enter to start."
	}
	line := func(index int, label, value string) string {
		prefix := "  "
		if m.actionField == index {
			prefix, value = "> ", value+"_"
		}
		return prefix + label + "  " + value
	}
	var values string
	if m.actionMode == actionPreview {
		values = strings.Join([]string{
			"Target: Preview Service",
			"",
			line(0, "Name        ", m.actionPreviewName),
			line(1, "Protocol    ", strings.ToUpper(m.actionProtocol)),
			line(2, "Service port", m.actionServicePort),
			line(3, "Local host  ", m.actionLocalHost),
			line(4, "Local port  ", firstNonEmpty(m.actionLocalPort, "0 (auto)")),
		}, "\n")
	} else {
		port := fmt.Sprintf("%d/%s", m.actionPort, strings.ToUpper(m.actionProtocol))
		values = strings.Join([]string{
			"Target: " + m.actionService,
			"",
			line(0, "Service port", port),
			line(1, "Local host  ", m.actionLocalHost),
			line(2, "Local port  ", firstNonEmpty(m.actionLocalPort, "0 (auto)")),
		}, "\n")
	}
	controls := consoleSubtle.Render("↑/↓ field   ←/→ select   Tab next")
	content := consoleSection.Render(title) + "\n\n" +
		description + "\n\n" +
		consoleValue.Render(values) + "\n\n" +
		controls + "\n\n" +
		consoleButton.Render(" Enter  Start ") + "  Esc cancel"
	box := consoleOverlayBox.Width(minInt(68, width-8)).Render(content)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

func trafficConsoleRow(
	label, kind string,
	index int,
	name, namespace, clusterIP, state string,
	targets []clientreverserelay.Target,
) consoleRow {
	detail := fmt.Sprintf("State: %s\nNamespace: %s\nCluster IP: %s", state, namespace, clusterIP)
	meta := clusterIP
	copyValue := clusterIP
	if len(targets) > 0 {
		target := targets[0]
		meta = fmt.Sprintf("%s:%d -> %s:%d", clusterIP, target.ServicePort, target.LocalHost, target.LocalPort)
		copyValue = fmt.Sprintf("%s:%d", clusterIP, target.ServicePort)
		detail += fmt.Sprintf(
			"\nProtocol: %s\nService port: %d\nLocal target: %s:%d",
			target.Protocol,
			target.ServicePort,
			target.LocalHost,
			target.LocalPort,
		)
	}
	return consoleRow{title: name, status: label, kind: kind, index: index, meta: meta, copy: copyValue, detail: detail}
}

func mirrorConsoleRow(index int, task clientmirror.Info) consoleRow {
	targets := make([]clientreverserelay.Target, 0, len(task.Targets))
	for _, target := range task.Targets {
		targets = append(targets, clientreverserelay.Target{
			ServicePort: target.ServicePort,
			Protocol:    target.Protocol,
			LocalHost:   target.LocalHost,
			LocalPort:   target.LocalPort,
		})
	}
	return trafficConsoleRow(
		"MIRROR",
		"mirror",
		index,
		task.Service,
		task.Namespace,
		task.ClusterIP,
		task.State,
		targets,
	)
}
