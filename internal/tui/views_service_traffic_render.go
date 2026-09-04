package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	clientmirror "github.com/fengqi-dev/kube-loop/internal/client/mirror"
	clientreverserelay "github.com/fengqi-dev/kube-loop/internal/client/reverserelay"
)

func (m Model) viewServiceTrafficAction(width, height int) string {
	title := "EXCHANGE"
	description := "Configure the Service port and local target, then press Enter to start."
	if m.action.mode == actionMirror {
		title = "MIRROR"
	}
	if m.action.mode == actionPreview {
		title = "CREATE PREVIEW"
		description = "Configure the Preview Service and local target, then press Enter to start."
	}
	line := func(index int, label, value string) string {
		prefix := "  "
		if m.action.field == index {
			prefix, value = "> ", value+"_"
		}
		return prefix + label + "  " + value
	}
	var values string
	if m.action.mode == actionPreview {
		values = strings.Join([]string{
			"Target: Preview Service",
			"",
			line(0, "Name        ", m.action.previewName),
			line(1, "Protocol    ", strings.ToUpper(m.action.protocol)),
			line(2, "Service port", m.action.servicePort),
			line(3, "Local host  ", m.action.localHost),
			line(4, "Local port  ", firstNonEmpty(m.action.localPort, "0 (auto)")),
		}, "\n")
	} else {
		port := fmt.Sprintf("%d/%s", m.action.port, strings.ToUpper(m.action.protocol))
		values = strings.Join([]string{
			"Target: " + m.action.service,
			"",
			line(0, "Service port", port),
			line(1, "Local host  ", m.action.localHost),
			line(2, "Local port  ", firstNonEmpty(m.action.localPort, "0 (auto)")),
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
	// Mirror's local targets are the shared relay target type, so the row
	// takes them as they are.
	targets := append([]clientreverserelay.Target(nil), task.Targets...)
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
