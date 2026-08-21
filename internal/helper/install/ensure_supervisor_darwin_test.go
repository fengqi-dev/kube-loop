//go:build darwin

package install

import (
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"testing"

	supervisorprotocol "github.com/fengqi-dev/kube-loop/internal/protocol/supervisor"
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

func TestWriteTemporaryTrustedCertificate(t *testing.T) {
	t.Parallel()
	content := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("certificate")})
	path, cleanup, err := writeTemporaryTrustedCertificate(content)
	if err != nil {
		t.Fatal(err)
	}
	if path == "" {
		t.Fatal("writeTemporaryTrustedCertificate() returned an empty path")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("temporary certificate mode = %o, want 600", info.Mode().Perm())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatal("temporary certificate content does not match")
	}
	cleanup()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary certificate remains after cleanup: %v", err)
	}
}

func TestWriteTemporaryTrustedCertificateRejectsPrivateKey(t *testing.T) {
	t.Parallel()
	content := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("private")})
	if _, _, err := writeTemporaryTrustedCertificate(content); err == nil {
		t.Fatal("writeTemporaryTrustedCertificate() accepted a private key")
	}
}

func TestCanUpdateWorkerThroughSupervisor(t *testing.T) {
	t.Parallel()
	healthy := supervisorprotocol.Response{
		Protocol: supervisorprotocol.Version,
		Channel:  "dev",
		Worker: supervisorprotocol.WorkerStatus{
			Installed: true,
			Running:   true,
		},
	}
	tests := []struct {
		name      string
		status    supervisorprotocol.Response
		statusErr error
		want      bool
	}{
		{name: "healthy installed worker", status: healthy, want: true},
		{
			name: "worker removed by uninstall",
			status: supervisorprotocol.Response{
				Protocol: supervisorprotocol.Version,
				Channel:  "dev",
			},
		},
		{
			name: "worker is not reachable",
			status: supervisorprotocol.Response{
				Protocol: supervisorprotocol.Version,
				Channel:  "dev",
				Worker: supervisorprotocol.WorkerStatus{
					Installed: true,
				},
			},
		},
		{name: "supervisor status failed", status: healthy, statusErr: errors.New("status failed")},
		{
			name: "supervisor protocol mismatch",
			status: supervisorprotocol.Response{
				Protocol: supervisorprotocol.Version + 1,
				Channel:  "dev",
				Worker:   healthy.Worker,
			},
		},
		{
			name: "supervisor channel mismatch",
			status: supervisorprotocol.Response{
				Protocol: supervisorprotocol.Version,
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
