package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	clientexchange "github.com/fengqi-dev/kube-loop/internal/client/exchange"
	clientmirror "github.com/fengqi-dev/kube-loop/internal/client/mirror"
	clientpreview "github.com/fengqi-dev/kube-loop/internal/client/preview"
)

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
