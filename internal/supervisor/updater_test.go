//go:build darwin

package supervisor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	supervisorprotocol "github.com/fengqi-dev/kube-loop/internal/protocol/supervisor"
)

type fakeWorker struct {
	config       Config
	status       supervisorprotocol.WorkerStatus
	startErrOnce error
	stops        int
	starts       int
}

func (w *fakeWorker) Status(context.Context) (supervisorprotocol.WorkerStatus, error) {
	status := w.status
	if digest, err := fileSHA256(w.config.WorkerBinaryPath); err == nil {
		status.Installed = true
		status.SHA256 = digest
	}
	return status, nil
}

func (w *fakeWorker) Stop(context.Context) error {
	w.stops++
	w.status.Running = false
	return nil
}

func (w *fakeWorker) Start(context.Context) error {
	w.starts++
	if w.startErrOnce != nil {
		err := w.startErrOnce
		w.startErrOnce = nil
		return err
	}
	w.status.Running = true
	w.status.CoreReady = true
	return nil
}

func TestValidateManifest(t *testing.T) {
	t.Parallel()
	payload := machOBytes("worker")
	valid := testManifest("dev", payload)
	tests := []struct {
		name   string
		mutate func(*supervisorprotocol.UpdateManifest)
	}{
		{name: "schema", mutate: func(value *supervisorprotocol.UpdateManifest) { value.SchemaVersion++ }},
		{name: "request ID", mutate: func(value *supervisorprotocol.UpdateManifest) { value.RequestID = "../bad" }},
		{name: "channel", mutate: func(value *supervisorprotocol.UpdateManifest) { value.Channel = "release" }},
		{name: "size", mutate: func(value *supervisorprotocol.UpdateManifest) { value.Size = 0 }},
		{name: "hash", mutate: func(value *supervisorprotocol.UpdateManifest) { value.SHA256 = "bad" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			manifest := valid
			test.mutate(&manifest)
			if err := validateManifest(Config{Channel: "dev"}, manifest); err == nil {
				t.Fatal("validateManifest unexpectedly succeeded")
			}
		})
	}
}

func TestUpdaterRejectsActiveSession(t *testing.T) {
	t.Parallel()
	config := testConfig(t)
	worker := &fakeWorker{config: config, status: supervisorprotocol.WorkerStatus{
		Installed: true, Running: true, CoreReady: true, ActiveSessions: []string{"tun-1"},
	}}
	updater := NewUpdater(config, worker, os.Getuid())
	updater.verifyArtifact = func(context.Context, string, supervisorprotocol.UpdateManifest) error { return nil }
	payload := machOBytes("new")
	response := updater.Update(t.Context(), testManifest(config.Channel, payload), bytes.NewReader(payload))
	if response.OK || response.Error == "" || worker.stops != 0 {
		t.Fatalf("response=%#v stops=%d", response, worker.stops)
	}
}

func TestUpdaterActivatesHealthyWorker(t *testing.T) {
	t.Parallel()
	config := testConfig(t)
	oldPayload := machOBytes("old")
	newPayload := machOBytes("new")
	writeExecutable(t, config.WorkerBinaryPath, oldPayload)
	worker := &fakeWorker{config: config, status: supervisorprotocol.WorkerStatus{
		Installed: true, Running: true, CoreReady: true, Version: "new", Protocol: 7,
	}}
	updater := NewUpdater(config, worker, os.Getuid())
	updater.verifyArtifact = func(context.Context, string, supervisorprotocol.UpdateManifest) error { return nil }
	manifest := testManifest(config.Channel, newPayload)
	manifest.Version = "new"
	manifest.WorkerProtocol = 7
	response := updater.Update(t.Context(), manifest, bytes.NewReader(newPayload))
	if !response.OK || response.RolledBack {
		t.Fatalf("Update response = %#v", response)
	}
	assertBytes(t, config.WorkerBinaryPath, newPayload)
	assertBytes(t, config.PreviousPath(), oldPayload)
}

func TestUpdaterRollsBackStartFailure(t *testing.T) {
	t.Parallel()
	config := testConfig(t)
	oldPayload := machOBytes("old")
	newPayload := machOBytes("new")
	writeExecutable(t, config.WorkerBinaryPath, oldPayload)
	worker := &fakeWorker{
		config:       config,
		startErrOnce: fmt.Errorf("start failed"),
		status: supervisorprotocol.WorkerStatus{
			Installed: true, Running: true, CoreReady: true, Version: "new", Protocol: 7,
		},
	}
	updater := NewUpdater(config, worker, os.Getuid())
	updater.verifyArtifact = func(context.Context, string, supervisorprotocol.UpdateManifest) error { return nil }
	updater.readyTimeout = 10 * time.Millisecond
	manifest := testManifest(config.Channel, newPayload)
	manifest.Version = "new"
	manifest.WorkerProtocol = 7
	response := updater.Update(t.Context(), manifest, bytes.NewReader(newPayload))
	if response.OK || !response.RolledBack {
		t.Fatalf("Update response = %#v", response)
	}
	assertBytes(t, config.WorkerBinaryPath, oldPayload)
}

func testConfig(t *testing.T) Config {
	t.Helper()
	dir := t.TempDir()
	return Config{
		Channel: "dev", WorkerLabel: "test.worker",
		WorkerBinaryPath: filepath.Join(dir, "bin", "worker"),
		WorkerPlistPath:  filepath.Join(dir, "worker.plist"),
		StateDir:         filepath.Join(dir, "state"),
	}
}

func testManifest(channel string, payload []byte) supervisorprotocol.UpdateManifest {
	sum := sha256.Sum256(payload)
	return supervisorprotocol.UpdateManifest{
		SchemaVersion: supervisorprotocol.SchemaVersion, RequestID: "request-1",
		Channel: channel, Version: "dev", WorkerProtocol: 7,
		MinimumSupervisorProtocol: supervisorprotocol.Version,
		Size:                      int64(len(payload)), SHA256: hex.EncodeToString(sum[:]),
	}
}

func machOBytes(value string) []byte {
	return append([]byte{0xcf, 0xfa, 0xed, 0xfe}, []byte(value)...)
}

func writeExecutable(t *testing.T, path string, payload []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, payload, 0o755); err != nil {
		t.Fatal(err)
	}
}

func assertBytes(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s = %x, want %x", path, got, want)
	}
}
