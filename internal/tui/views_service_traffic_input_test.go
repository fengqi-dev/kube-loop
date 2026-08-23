package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestUpdateServiceTrafficActionPreviewInput(t *testing.T) {
	t.Parallel()

	model := Model{
		actionMode: actionPreview, actionProtocol: "tcp", actionField: 1,
		actionPreviewName: "preview", actionServicePort: "8080",
		actionLocalHost: "127.0.0.1", actionLocalPort: "9090",
	}
	next, _ := model.updateServiceTrafficAction(tea.KeyMsg{Type: tea.KeyRight})
	model = requireModel(next)
	if model.actionProtocol != "udp" {
		t.Fatalf("protocol = %q, want udp", model.actionProtocol)
	}
	next, _ = model.updateServiceTrafficAction(tea.KeyMsg{Type: tea.KeyTab})
	model = requireModel(next)
	if model.actionField != 2 {
		t.Fatalf("field = %d, want 2", model.actionField)
	}
	model.actionServicePort = ""
	next, _ = model.updateServiceTrafficAction(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("8x0")})
	model = requireModel(next)
	if model.actionServicePort != "80" {
		t.Fatalf("service port input = %q", model.actionServicePort)
	}
	next, _ = model.updateServiceTrafficAction(tea.KeyMsg{Type: tea.KeyBackspace})
	model = requireModel(next)
	if model.actionServicePort != "8" {
		t.Fatalf("service port after backspace = %q", model.actionServicePort)
	}
	model.actionServicePort = "8080"
	next, command := model.updateServiceTrafficAction(tea.KeyMsg{Type: tea.KeyEnter})
	model = requireModel(next)
	if command == nil || !model.loading || model.err != "" {
		t.Fatalf("valid preview submit: command=%v loading=%t error=%q", command != nil, model.loading, model.err)
	}
}

func TestUpdateServiceTrafficActionSelectsServicePort(t *testing.T) {
	t.Parallel()

	model := Model{
		actionMode: actionExchange, actionField: 0, actionPortIndex: 0,
		actionPorts: []actionPortOption{
			{Name: "http", Port: 8080, Protocol: "tcp"},
			{Name: "dns", Port: 5353, Protocol: "udp"},
		},
	}
	next, _ := model.updateServiceTrafficAction(tea.KeyMsg{Type: tea.KeyRight})
	model = requireModel(next)
	if model.actionPortIndex != 1 || model.actionPort != 5353 || model.actionProtocol != "udp" ||
		model.actionLocalPort != "5353" {
		t.Fatalf("selected port state = %#v", model)
	}
	next, _ = model.updateServiceTrafficAction(tea.KeyMsg{Type: tea.KeyLeft})
	model = requireModel(next)
	if model.actionPortIndex != 0 || model.actionPort != 8080 || model.actionProtocol != "tcp" {
		t.Fatalf("wrapped port state = %#v", model)
	}
}

func TestUpdateServiceTrafficActionRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	model := Model{actionMode: actionPreview, actionProtocol: "tcp"}
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
		{name: "local host", model: Model{actionMode: actionExchange}, want: "local host"},
		{
			name:  "preview name",
			model: Model{actionMode: actionPreview, actionLocalHost: "127.0.0.1", actionServicePort: "8080"},
			want:  "preview name",
		},
		{
			name: "service port",
			model: Model{
				actionMode: actionPreview, actionPreviewName: "preview", actionLocalHost: "127.0.0.1",
				actionServicePort: "0",
			},
			want: "service port",
		},
		{
			name:  "local port",
			model: Model{actionMode: actionExchange, actionLocalHost: "127.0.0.1", actionLocalPort: "70000"},
			want:  "local port",
		},
		{
			name:  "valid exchange",
			model: Model{actionMode: actionExchange, actionLocalHost: "127.0.0.1"},
		},
		{
			name: "valid preview",
			model: Model{
				actionMode: actionPreview, actionPreviewName: "preview", actionLocalHost: "127.0.0.1",
				actionServicePort: "8080", actionLocalPort: "9090",
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
