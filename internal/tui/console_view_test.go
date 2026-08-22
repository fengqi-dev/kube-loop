package tui

import "testing"

func TestViewConsoleFooterStates(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Model)
		want   []string
	}{
		{
			name: "default hint",
			want: []string{"enter connect", "q quit"},
		},
		{
			name: "command input",
			mutate: func(model *Model) {
				model.console.inputMode = inputCommand
				model.console.inputText = "sessions"
			},
			want: []string{":", "sessions", "Enter run", "Esc cancel"},
		},
		{
			name: "filter input",
			mutate: func(model *Model) {
				model.console.inputMode = inputFilter
				model.console.inputText = "api"
			},
			want: []string{"/", "api", "Enter keep", "Esc clear"},
		},
		{
			name: "loading takes priority",
			mutate: func(model *Model) {
				model.loading = true
				model.err = "request failed"
				model.status = "connected"
			},
			want: []string{"Working"},
		},
		{
			name: "error takes priority over status",
			mutate: func(model *Model) {
				model.err = "request failed"
				model.status = "connected"
			},
			want: []string{"request failed"},
		},
		{
			name: "status",
			mutate: func(model *Model) {
				model.status = "connected"
			},
			want: []string{"connected"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := newConsoleTestModel(tabConnection, 100, 28)
			if test.mutate != nil {
				test.mutate(&model)
			}
			assertConsoleContains(t, model.viewConsoleFooter(), test.want...)
		})
	}
}
