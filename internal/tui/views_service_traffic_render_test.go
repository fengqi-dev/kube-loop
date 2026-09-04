package tui

import (
	"strings"
	"testing"

	clientmirror "github.com/fengqi-dev/kube-loop/internal/client/mirror"
	clientreverserelay "github.com/fengqi-dev/kube-loop/internal/client/reverserelay"
)

func TestViewServiceTrafficAction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		model Model
		want  []string
	}{
		{
			name: "exchange",
			model: Model{
				action: actionState{
					mode:      actionExchange,
					service:   "api",
					port:      8080,
					protocol:  "tcp",
					localHost: "127.0.0.1",
				},
			},
			want: []string{"EXCHANGE", "Target: api", "8080/TCP", "0 (auto)"},
		},
		{
			name: "mirror",
			model: Model{
				action: actionState{
					mode:      actionMirror,
					service:   "api",
					port:      8080,
					protocol:  "tcp",
					localHost: "127.0.0.1",
				},
			},
			want: []string{"MIRROR", "Target: api"},
		},
		{
			name: "preview",
			model: Model{
				action: actionState{
					mode:        actionPreview,
					previewName: "preview",
					protocol:    "udp",
					servicePort: "5353",
					localHost:   "127.0.0.1",
					localPort:   "5353",
				},
			},
			want: []string{"CREATE PREVIEW", "Preview Service", "preview", "UDP", "5353"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			view := test.model.viewServiceTrafficAction(90, 28)
			for _, want := range test.want {
				if !strings.Contains(view, want) {
					t.Fatalf("view missing %q: %q", want, view)
				}
			}
		})
	}
}

func TestTrafficConsoleRow(t *testing.T) {
	t.Parallel()

	row := trafficConsoleRow(
		"EXCHANGE",
		"exchange",
		2,
		"api",
		"default",
		"10.96.0.10",
		"running",
		[]clientreverserelay.Target{{
			ServicePort: 8080, Protocol: "tcp", LocalHost: "127.0.0.1", LocalPort: 18080,
		}},
	)
	if row.title != "api" || row.status != "EXCHANGE" || row.copy != "10.96.0.10:8080" ||
		!strings.Contains(row.meta, "127.0.0.1:18080") || !strings.Contains(row.detail, "Protocol: tcp") {
		t.Fatalf("traffic row = %#v", row)
	}
	empty := trafficConsoleRow("PREVIEW", "preview", 0, "preview", "default", "", "pending", nil)
	if empty.meta != "" || empty.copy != "" || !strings.Contains(empty.detail, "State: pending") {
		t.Fatalf("empty traffic row = %#v", empty)
	}
}

func TestMirrorConsoleRow(t *testing.T) {
	t.Parallel()

	row := mirrorConsoleRow(1, clientmirror.Info{
		Service: "api", Namespace: "default", ClusterIP: "10.96.0.10", State: "running",
		Targets: []clientmirror.LocalTarget{{
			ServicePort: 8080, Protocol: "tcp", LocalHost: "127.0.0.1", LocalPort: 18080,
		}},
	})
	if row.kind != "mirror" || row.status != "MIRROR" || row.index != 1 || row.title != "api" {
		t.Fatalf("mirror row = %#v", row)
	}
}
