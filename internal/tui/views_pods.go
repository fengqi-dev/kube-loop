package tui

import (
	"context"
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
	case keyDown, "j":
		if m.cursor < len(m.pods)-1 {
			m.cursor++
		}
	case "n":
		return m, m.cycleNamespace()
	case "f", keyEnter:
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
			m.actionPorts = append(
				m.actionPorts,
				actionPortOption{Name: port.Name, Port: port.Port, Protocol: port.Protocol},
			)
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
	if msg.String() == keyEsc {
		m.actionMode, m.actionCommand, m.actionPorts = actionNone, "", nil
		return m, nil
	}
	switch m.actionMode {
	case actionExec:
		return m.updateExecAction(msg)
	case actionExchange, actionMirror, actionPreview:
		return m.updateServiceTrafficAction(msg)
	case actionNone, actionPortForward:
		return m.updatePortForwardAction(msg)
	}
	return m, nil
}

func (m Model) updateExecAction(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyEnter:
		if strings.TrimSpace(m.actionCommand) == "" {
			m.err = "command is required"
			return m, nil
		}
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, m.startExec())
	case keyBackspace:
		m.actionCommand = trimLastRune(m.actionCommand)
	default:
		if len(msg.Runes) > 0 {
			m.actionCommand += string(msg.Runes)
		}
	}
	return m, nil
}

func (m Model) updatePortForwardAction(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyTab, keyShiftTab:
		m.actionField = 1 - m.actionField
		return m, nil
	case "up", "k":
		m.actionField = (m.actionField - 1 + 2) % 2
		return m, nil
	case keyDown, "j":
		m.actionField = (m.actionField + 1) % 2
		return m, nil
	case keyLeft, "h":
		if m.actionField == 0 && len(m.actionPorts) > 1 {
			m.actionPortIndex = (m.actionPortIndex - 1 + len(m.actionPorts)) % len(m.actionPorts)
			m.selectActionPort()
		}
		return m, nil
	case keyRight, "l":
		if m.actionField == 0 && len(m.actionPorts) > 1 {
			m.actionPortIndex = (m.actionPortIndex + 1) % len(m.actionPorts)
			m.selectActionPort()
		}
		return m, nil
	case keyBackspace:
		if m.actionField == 1 {
			m.actionLocalPort = trimLastRune(m.actionLocalPort)
		}
		return m, nil
	case keyEnter:
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
		kind, name := resourceKindPod, m.actionPod
		if m.actionService != "" {
			kind, name = resourceKindService, m.actionService
		}
		localPort, err := strconv.ParseUint(firstNonEmpty(strings.TrimSpace(m.actionLocalPort), "0"), 10, 16)
		if err != nil {
			return portForwardStartedMsg{err: fmt.Errorf("local port is invalid")}
		}
		request := clientportforward.Request{
			ProfileID:  profile.ID,
			Kind:       kind,
			Name:       name,
			Protocol:   m.actionProtocol,
			RemotePort: uint16(m.actionPort), //nolint:gosec // Selected Kubernetes ports are validated as uint16.
			LocalPort:  uint16(localPort),
		}
		info, err := m.state.forwards.Start(m.state.ctx, profile, session, request)
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
		request := clientpodssh.Request{
			ProfileID:  profile.ID,
			Namespace:  pod.Namespace,
			Pod:        pod.Name,
			Container:  container,
			PodIP:      pod.PodIP,
			Ready:      pod.Ready,
			Containers: pod.Containers,
		}
		info, err := m.state.podSSH.Start(m.state.ctx, profile, session, request)
		return podSSHStartedMsg{info: info, err: err}
	}
}

func openPodSSH(ctx context.Context, command string) tea.Cmd {
	args, err := shellquote.Split(command)
	if err != nil || len(args) == 0 || args[0] != taskKindSSH {
		return func() tea.Msg {
			if err == nil {
				err = fmt.Errorf("pod SSH command is empty or unsupported")
			}
			return podSSHExitedMsg{err: err}
		}
	}
	process := exec.CommandContext( //nolint:gosec // Executable is fixed; arguments come from the Pod SSH manager.
		ctx,
		taskKindSSH,
		args[1:]...,
	)
	return tea.ExecProcess(process, func(err error) tea.Msg {
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
		spec := clientremote.ExecSpec{
			Pod:       m.actionPod,
			Container: m.actionContainer,
			Command:   []string{"/bin/sh", "-lc", command},
			TTY:       false,
		}
		task, err := m.state.execs.Start(m.state.ctx, profile, session, spec)
		return execStartedMsg{task: task, command: command, err: err}
	}
}
