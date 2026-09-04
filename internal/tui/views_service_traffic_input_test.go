package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestUpdateServiceTrafficActionPreviewInput(t *testing.T) {
	t.Parallel()

	model := Model{
		action: actionState{
			mode:        actionPreview,
			protocol:    "tcp",
			field:       1,
			previewName: "preview",
			servicePort: "8080",
			localHost:   "127.0.0.1",
			localPort:   "9090",
		},
	}
	next, _ := model.updateServiceTrafficAction(tea.KeyMsg{Type: tea.KeyRight})
	model = requireModel(next)
	if model.action.protocol != "udp" {
		t.Fatalf("protocol = %q, want udp", model.action.protocol)
	}
	next, _ = model.updateServiceTrafficAction(tea.KeyMsg{Type: tea.KeyTab})
	model = requireModel(next)
	if model.action.field != 2 {
		t.Fatalf("field = %d, want 2", model.action.field)
	}
	model.action.servicePort = ""
	next, _ = model.updateServiceTrafficAction(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("8x0")})
	model = requireModel(next)
	if model.action.servicePort != "80" {
		t.Fatalf("service port input = %q", model.action.servicePort)
	}
	next, _ = model.updateServiceTrafficAction(tea.KeyMsg{Type: tea.KeyBackspace})
	model = requireModel(next)
	if model.action.servicePort != "8" {
		t.Fatalf("service port after backspace = %q", model.action.servicePort)
	}
	model.action.servicePort = "8080"
	next, command := model.updateServiceTrafficAction(tea.KeyMsg{Type: tea.KeyEnter})
	model = requireModel(next)
	if command == nil || !model.loading || model.err != "" {
		t.Fatalf("valid preview submit: command=%v loading=%t error=%q", command != nil, model.loading, model.err)
	}
}

func TestUpdateServiceTrafficActionSelectsServicePort(t *testing.T) {
	t.Parallel()

	model := Model{action: actionState{mode: actionExchange, field: 0, portIndex: 0, ports: []actionPortOption{
		{Name: "http", Port: 8080, Protocol: "tcp"},
		{Name: "dns", Port: 5353, Protocol: "udp"},
	}}}
	next, _ := model.updateServiceTrafficAction(tea.KeyMsg{Type: tea.KeyRight})
	model = requireModel(next)
	if model.action.portIndex != 1 || model.action.port != 5353 || model.action.protocol != "udp" ||
		model.action.localPort != "5353" {
		t.Fatalf("selected port state = %#v", model)
	}
	next, _ = model.updateServiceTrafficAction(tea.KeyMsg{Type: tea.KeyLeft})
	model = requireModel(next)
	if model.action.portIndex != 0 || model.action.port != 8080 || model.action.protocol != "tcp" {
		t.Fatalf("wrapped port state = %#v", model)
	}
}

func TestUpdateServiceTrafficActionRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	model := Model{action: actionState{mode: actionPreview, protocol: "tcp"}}
	next, command := model.updateServiceTrafficAction(tea.KeyMsg{Type: tea.KeyEnter})
	model = requireModel(next)
	if command != nil || model.loading || !strings.Contains(model.err, "local host") {
		t.Fatalf("invalid submit: command=%v loading=%t error=%q", command != nil, model.loading, model.err)
	}
}

func TestValidateServiceTrafficAction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		model Model
		want  string
	}{
		{name: "local host", model: Model{action: actionState{mode: actionExchange}}, want: "local host"},
		{
			name:  "preview name",
			model: Model{action: actionState{mode: actionPreview, localHost: "127.0.0.1", servicePort: "8080"}},
			want:  "preview name",
		},
		{
			name: "service port",
			model: Model{
				action: actionState{
					mode:        actionPreview,
					previewName: "preview",
					localHost:   "127.0.0.1",
					servicePort: "0",
				},
			},
			want: "service port",
		},
		{
			name:  "local port",
			model: Model{action: actionState{mode: actionExchange, localHost: "127.0.0.1", localPort: "70000"}},
			want:  "local port",
		},
		{
			name:  "valid exchange",
			model: Model{action: actionState{mode: actionExchange, localHost: "127.0.0.1"}},
		},
		{
			name: "valid preview",
			model: Model{
				action: actionState{
					mode:        actionPreview,
					previewName: "preview",
					localHost:   "127.0.0.1",
					servicePort: "8080",
					localPort:   "9090",
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := test.model.validateServiceTrafficAction()
			if test.want == "" && err != nil {
				t.Fatal(err)
			}
			if test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
				t.Fatalf("validation error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestParseActionPort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		value     string
		allowZero bool
		want      uint16
		wantError bool
	}{
		{name: "empty automatic", allowZero: true},
		{name: "zero automatic", value: "0", allowZero: true},
		{name: "valid", value: " 8080 ", want: 8080},
		{name: "required zero", value: "0", wantError: true},
		{name: "overflow", value: "65536", allowZero: true, wantError: true},
		{name: "text", value: "http", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseActionPort(test.value, test.allowZero)
			if (err != nil) != test.wantError || got != test.want {
				t.Fatalf("parseActionPort(%q, %t) = %d, %v", test.value, test.allowZero, got, err)
			}
		})
	}
}
