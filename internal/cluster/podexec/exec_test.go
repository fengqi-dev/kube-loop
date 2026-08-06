package podexec

import (
	"context"
	"strings"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/podssh"
)

func TestExecValidatesRequestBeforeCreatingClient(t *testing.T) {
	tests := []struct {
		name    string
		target  podssh.Target
		command []string
		want    string
	}{
		{
			name:    "missing target",
			command: []string{"true"},
			want:    "context, namespace, and pod are required",
		},
		{
			name: "missing command",
			target: podssh.Target{
				Context: "context", Namespace: "default", Pod: "pod",
			},
			want: "exec command is required",
		},
		{
			name: "missing config",
			target: podssh.Target{
				Context: "context", Namespace: "default", Pod: "pod",
			},
			command: []string{"true"},
			want:    "REST config is required",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := Exec(context.Background(), nil, test.target, test.command, podssh.Streams{})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Exec() error = %v, want %q", err, test.want)
			}
		})
	}
}
