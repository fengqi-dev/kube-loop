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
		m.action.mode, m.action.pod, m.action.service = actionPortForward, pod.Name, ""
		m.action.ports = make([]actionPortOption, 0, len(pod.Ports))
		for _, port := range pod.Ports {
			m.action.ports = append(
				m.action.ports,
				actionPortOption{Name: port.Name, Port: port.Port, Protocol: port.Protocol},
			)
		}
		m.action.portIndex, m.action.localPort, m.action.field = 0, "0", 0
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
		m.action.mode, m.action.command, m.action.ports = actionNone, "", nil
		return m, nil
	}
	switch m.action.mode {
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
		if strings.TrimSpace(m.action.command) == "" {
			m.err = "command is required"
			return m, nil
		}
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, m.startExec())
	case keyBackspace:
		m.action.command = trimLastRune(m.action.command)
	default:
		if len(msg.Runes) > 0 {
			m.action.command += string(msg.Runes)
		}
	}
	return m, nil
}

func (m Model) updatePortForwardAction(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyTab, keyShiftTab:
		m.action.field = 1 - m.action.field
		return m, nil
	case "up", "k":
		m.action.field = (m.action.field - 1 + 2) % 2
		return m, nil
	case keyDown, "j":
		m.action.field = (m.action.field + 1) % 2
		return m, nil
	case keyLeft, "h":
		if m.action.field == 0 && len(m.action.ports) > 1 {
			m.action.portIndex = (m.action.portIndex - 1 + len(m.action.ports)) % len(m.action.ports)
			m.selectActionPort()
		}
		return m, nil
	case keyRight, "l":
		if m.action.field == 0 && len(m.action.ports) > 1 {
			m.action.portIndex = (m.action.portIndex + 1) % len(m.action.ports)
			m.selectActionPort()
		}
		return m, nil
	case keyBackspace:
		if m.action.field == 1 {
			m.action.localPort = trimLastRune(m.action.localPort)
		}
		return m, nil
	case keyEnter:
		localPort := strings.TrimSpace(m.action.localPort)
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
		if m.action.field == 1 && len(msg.Runes) > 0 {
			for _, character := range msg.Runes {
				if character >= '0' && character <= '9' && len(m.action.localPort) < 5 {
					if m.action.localPort == "0" {
						m.action.localPort = ""
					}
					m.action.localPort += string(character)
				}
			}
		}
	}
	return m, nil
}

func (m *Model) selectActionPort() {
	if m.action.portIndex < 0 || m.action.portIndex >= len(m.action.ports) {
		return
	}
	selected := m.action.ports[m.action.portIndex]
	m.action.port, m.action.protocol = selected.Port, selected.Protocol
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
		kind, name := resourceKindPod, m.action.pod
		if m.action.service != "" {
			kind, name = resourceKindService, m.action.service
		}
		localPort, err := strconv.ParseUint(firstNonEmpty(strings.TrimSpace(m.action.localPort), "0"), 10, 16)
		if err != nil {
			return portForwardStartedMsg{err: fmt.Errorf("local port is invalid")}
		}
		request := clientportforward.Request{
			ProfileID:  profile.ID,
			Kind:       kind,
			Name:       name,
			Protocol:   m.action.protocol,
			RemotePort: uint16(m.action.port), //nolint:gosec // Selected Kubernetes ports are validated as uint16.
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
		command := strings.TrimSpace(m.action.command)
		spec := clientremote.ExecSpec{
			Pod:       m.action.pod,
			Container: m.action.container,
			Command:   []string{"/bin/sh", "-lc", command},
			TTY:       false,
		}
		task, err := m.state.execs.Start(m.state.ctx, profile, session, spec)
		return execStartedMsg{task: task, command: command, err: err}
	}
}
