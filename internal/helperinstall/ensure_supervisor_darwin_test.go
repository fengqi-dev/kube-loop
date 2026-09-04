//go:build darwin

package helperinstall

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/protocol/supervisor"
)

func TestInstalledCoreMatches(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		installedContent []byte
		createInstalled  bool
		want             bool
	}{
		{
			name:             "same content at different path",
			installedContent: []byte("same-core"),
			createInstalled:  true,
			want:             true,
		},
		{name: "different content", installedContent: []byte("old-core"), createInstalled: true},
		{name: "installed core missing"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			directory := t.TempDir()
			source := filepath.Join(directory, "cached-sing-box")
			installed := filepath.Join(directory, "system-sing-box")
			if err := os.WriteFile(source, []byte("same-core"), 0o700); err != nil {
				t.Fatal(err)
			}
			if test.createInstalled {
				if err := os.WriteFile(installed, test.installedContent, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if got := installedCoreMatches(source, installed); got != test.want {
				t.Fatalf("installedCoreMatches() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestCanUpdateWorkerThroughSupervisor(t *testing.T) {
	t.Parallel()
	healthy := supervisor.Response{
		Protocol: supervisor.Version,
		Channel:  "dev",
		Worker: supervisor.WorkerStatus{
			Installed: true,
			Running:   true,
		},
	}
	tests := []struct {
		name      string
		status    supervisor.Response
		statusErr error
		want      bool
	}{
		{name: "healthy installed worker", status: healthy, want: true},
		{
			name: "worker removed by uninstall",
			status: supervisor.Response{
				Protocol: supervisor.Version,
				Channel:  "dev",
			},
		},
		{
			name: "worker is not reachable",
			status: supervisor.Response{
				Protocol: supervisor.Version,
				Channel:  "dev",
				Worker: supervisor.WorkerStatus{
					Installed: true,
				},
			},
		},
		{name: "supervisor status failed", status: healthy, statusErr: errors.New("status failed")},
		{
			name: "supervisor protocol mismatch",
			status: supervisor.Response{
				Protocol: supervisor.Version + 1,
				Channel:  "dev",
				Worker:   healthy.Worker,
			},
		},
		{
			name: "supervisor channel mismatch",
			status: supervisor.Response{
				Protocol: supervisor.Version,
				Channel:  "release",
				Worker:   healthy.Worker,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := canUpdateWorkerThroughSupervisor(test.status, test.statusErr, "dev", "same", "same")
			if got != test.want {
				t.Fatalf("canUpdateWorkerThroughSupervisor() = %v, want %v", got, test.want)
			}
		})
	}

	if canUpdateWorkerThroughSupervisor(healthy, nil, "dev", "installed", "bundled") {
		t.Fatal("canUpdateWorkerThroughSupervisor() accepted a stale supervisor binary")
	}
}
