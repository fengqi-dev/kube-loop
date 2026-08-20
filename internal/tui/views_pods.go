package tui

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kballard/go-shellquote"

	clientdataplane "github.com/fengqi-dev/kube-loop/internal/client/dataplane"
	clientpodssh "github.com/fengqi-dev/kube-loop/internal/client/podssh"
	clientportforward "github.com/fengqi-dev/kube-loop/internal/client/portforward"
	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
)

func (m Model) updatePods(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.pods)-1 {
			m.cursor++
		}
	case "n":
		return m, m.cycleNamespace()
	case "f", "enter":
		row, ok := m.selectedConsoleRow()
		if !ok || row.index >= len(m.pods) {
			return m, nil
		}
		pod := m.pods[row.index]
		if len(pod.Ports) == 0 {
			m.err = "pod exposes no ports"
			return m, nil
		}
		m.actionMode, m.actionPod, m.actionService = actionPortForward, pod.Name, ""
		m.actionPorts = make([]actionPortOption, 0, len(pod.Ports))
		for _, port := range pod.Ports {
			m.actionPorts = append(m.actionPorts, actionPortOption{Name: port.Name, Port: port.Port, Protocol: port.Protocol})
		}
		m.actionPortIndex, m.actionLocalPort, m.actionField = 0, "0", 0
		m.selectActionPort()
		m.err, m.status = "", ""
		return m, nil
	case "s":
		row, ok := m.selectedConsoleRow()
		if !ok || row.index >= len(m.pods) {
			return m, nil
		}
		if m.dataPlaneStatus.Mode != string(clientdataplane.ModeTUN) {
			m.err = "Pod SSH requires TUN mode"
			return m, nil
		}
		m.loading, m.status = true, "Starting Pod SSH..."
		return m, tea.Batch(m.spinner.Tick, m.startPodSSH(m.pods[row.index]))
	}
	return m, nil
}

func (m Model) updateAction(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "esc" {
		m.actionMode, m.actionCommand, m.actionPorts = actionNone, "", nil
		return m, nil
	}
	if m.actionMode == actionExec {
		switch msg.String() {
		case "enter":
			if strings.TrimSpace(m.actionCommand) == "" {
				m.err = "command is required"
				return m, nil
			}
			m.loading = true
			return m, tea.Batch(m.spinner.Tick, m.startExec())
		case "backspace":
			m.actionCommand = trimLastRune(m.actionCommand)
			return m, nil
		default:
			if len(msg.Runes) > 0 {
				m.actionCommand += string(msg.Runes)
			}
			return m, nil
		}
	}
	if m.actionMode == actionExchange || m.actionMode == actionMirror || m.actionMode == actionPreview {
		return m.updateServiceTrafficAction(msg)
	}
	switch msg.String() {
	case "tab", "shift+tab":
		m.actionField = 1 - m.actionField
		return m, nil
	case "up", "k":
		m.actionField = (m.actionField - 1 + 2) % 2
		return m, nil
	case "down", "j":
		m.actionField = (m.actionField + 1) % 2
		return m, nil
	case "left", "h":
		if m.actionField == 0 && len(m.actionPorts) > 1 {
			m.actionPortIndex = (m.actionPortIndex - 1 + len(m.actionPorts)) % len(m.actionPorts)
			m.selectActionPort()
		}
		return m, nil
	case "right", "l":
		if m.actionField == 0 && len(m.actionPorts) > 1 {
			m.actionPortIndex = (m.actionPortIndex + 1) % len(m.actionPorts)
			m.selectActionPort()
		}
		return m, nil
	case "backspace":
		if m.actionField == 1 {
			m.actionLocalPort = trimLastRune(m.actionLocalPort)
		}
		return m, nil
	case "enter":
		localPort := strings.TrimSpace(m.actionLocalPort)
		if localPort == "" {
			localPort = "0"
		}
		if _, err := strconv.ParseUint(localPort, 10, 16); err != nil {
			m.err = "local port must be between 0 and 65535"
			return m, nil
		}
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, m.startPortForward())
	default:
		if m.actionField == 1 && len(msg.Runes) > 0 {
			for _, character := range msg.Runes {
				if character >= '0' && character <= '9' && len(m.actionLocalPort) < 5 {
					if m.actionLocalPort == "0" {
						m.actionLocalPort = ""
					}
					m.actionLocalPort += string(character)
				}
			}
		}
	}
	return m, nil
}

func (m *Model) selectActionPort() {
	if m.actionPortIndex < 0 || m.actionPortIndex >= len(m.actionPorts) {
		return
	}
	selected := m.actionPorts[m.actionPortIndex]
	m.actionPort, m.actionProtocol = selected.Port, selected.Protocol
}

func (m Model) startPortForward() tea.Cmd {
	return func() tea.Msg {
		profile, ok := m.state.ActiveProfile()
		if !ok {
			return portForwardStartedMsg{err: fmt.Errorf("no active server")}
		}
		session, err := m.state.sessions.Current(profile.ID)
		if err != nil {
			return portForwardStartedMsg{err: err}
		}
		kind, name := "pod", m.actionPod
		if m.actionService != "" {
			kind, name = "service", m.actionService
		}
		localPort, err := strconv.ParseUint(firstNonEmpty(strings.TrimSpace(m.actionLocalPort), "0"), 10, 16)
		if err != nil {
			return portForwardStartedMsg{err: fmt.Errorf("local port is invalid")}
		}
		info, err := m.state.forwards.Start(m.state.ctx, profile, session, clientportforward.Request{ProfileID: profile.ID, Kind: kind, Name: name, Protocol: m.actionProtocol, RemotePort: uint16(m.actionPort), LocalPort: uint16(localPort)})
		return portForwardStartedMsg{info: info, err: err}
	}
}

func (m Model) startPodSSH(pod clientremote.Pod) tea.Cmd {
	return func() tea.Msg {
		profile, ok := m.state.ActiveProfile()
		if !ok {
			return podSSHStartedMsg{err: fmt.Errorf("no active server")}
		}
		session, err := m.state.sessions.Current(profile.ID)
		if err != nil {
			return podSSHStartedMsg{err: err}
		}
		container := ""
		if len(pod.Containers) > 0 {
			container = pod.Containers[0]
		}
		info, err := m.state.podSSH.Start(m.state.ctx, profile, session, clientpodssh.Request{ProfileID: profile.ID, Namespace: pod.Namespace, Pod: pod.Name, Container: container, PodIP: pod.PodIP, Ready: pod.Ready, Containers: pod.Containers})
		return podSSHStartedMsg{info: info, err: err}
	}
}

func openPodSSH(command string) tea.Cmd {
	args, err := shellquote.Split(command)
	if err != nil || len(args) == 0 {
		return func() tea.Msg {
			if err == nil {
				err = fmt.Errorf("Pod SSH command is empty")
			}
			return podSSHExitedMsg{err: err}
		}
	}
	return tea.ExecProcess(exec.Command(args[0], args[1:]...), func(err error) tea.Msg {
		return podSSHExitedMsg{err: err}
	})
}

type podSSHExitedMsg struct{ err error }

func (m Model) startExec() tea.Cmd {
	return func() tea.Msg {
		profile, ok := m.state.ActiveProfile()
		if !ok {
			return execStartedMsg{err: fmt.Errorf("no active server")}
		}
		session, err := m.state.sessions.Current(profile.ID)
		if err != nil {
			return execStartedMsg{err: err}
		}
		command := strings.TrimSpace(m.actionCommand)
		task, err := m.state.execs.Start(m.state.ctx, profile, session, clientremote.ExecSpec{Pod: m.actionPod, Container: m.actionContainer, Command: []string{"/bin/sh", "-lc", command}, TTY: false})
		return execStartedMsg{task: task, command: command, err: err}
	}
}
