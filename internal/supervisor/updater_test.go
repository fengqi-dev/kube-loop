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
	"strings"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/supervisor"
	"github.com/fengqi-dev/kube-loop/internal/utils"
)

type fakeWorker struct {
	config       Config
	status       supervisor.WorkerStatus
	startErrOnce error
	stopErrOnce  error
	stops        int
	starts       int
	statusCalls  int
	readyAfter   int
}

func (w *fakeWorker) Status(context.Context) (supervisor.WorkerStatus, error) {
	w.statusCalls++
	if w.readyAfter > 0 && w.statusCalls >= w.readyAfter {
		w.status.CoreReady = true
	}
	status := w.status
	if digest, err := utils.FileSHA256(w.config.WorkerBinaryPath); err == nil {
		status.Installed = true
		status.SHA256 = digest
	}
	return status, nil
}

func (w *fakeWorker) Stop(context.Context) error {
	w.stops++
	if w.stopErrOnce != nil {
		err := w.stopErrOnce
		w.stopErrOnce = nil
		return err
	}
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
	w.status.CoreReady = w.readyAfter == 0
	return nil
}

func TestValidateManifest(t *testing.T) {
	t.Parallel()
	payload := machOBytes("worker")
	valid := testManifest("dev", payload)
	tests := []struct {
		name   string
		mutate func(*supervisor.UpdateManifest)
	}{
		{name: "schema", mutate: func(value *supervisor.UpdateManifest) { value.SchemaVersion++ }},
		{name: "request ID", mutate: func(value *supervisor.UpdateManifest) { value.RequestID = "../bad" }},
		{name: "channel", mutate: func(value *supervisor.UpdateManifest) { value.Channel = "release" }},
		{name: "size", mutate: func(value *supervisor.UpdateManifest) { value.Size = 0 }},
		{name: "hash", mutate: func(value *supervisor.UpdateManifest) { value.SHA256 = "bad" }},
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
	worker := &fakeWorker{config: config, status: supervisor.WorkerStatus{
		Installed: true, Running: true, CoreReady: true, ActiveSessions: []string{"tun-1"},
	}}
	updater := NewUpdater(config, worker, os.Getuid())
	updater.verifyArtifact = func(context.Context, string, supervisor.UpdateManifest) error { return nil }
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
	worker := &fakeWorker{config: config, status: supervisor.WorkerStatus{
		Installed: true, Running: true, CoreReady: true, Version: "new", Protocol: 7,
	}}
	updater := NewUpdater(config, worker, os.Getuid())
	updater.verifyArtifact = func(context.Context, string, supervisor.UpdateManifest) error { return nil }
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

func TestUpdaterReportsJournalCleanupFailure(t *testing.T) {
	tests := []struct {
		name           string
		startErr       error
		wantRolledBack bool
	}{
		{name: "successful activation"},
		{
			name: "successful rollback", startErr: fmt.Errorf("start failed"),
			wantRolledBack: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := testConfig(t)
			oldPayload := machOBytes("old")
			newPayload := machOBytes("new")
			writeExecutable(t, config.WorkerBinaryPath, oldPayload)
			worker := &fakeWorker{
				config: config, startErrOnce: test.startErr,
				status: supervisor.WorkerStatus{
					Installed: true, Running: true, CoreReady: true,
					Version: "new", Protocol: 7,
				},
			}
			updater := NewUpdater(config, worker, os.Getuid())
			updater.verifyArtifact = func(
				context.Context,
				string,
				supervisor.UpdateManifest,
			) error {
				return nil
			}
			cleanupErr := fmt.Errorf("journal cleanup failed")
			updater.removeFile = func(path string) error {
				if path == config.JournalPath() {
					return cleanupErr
				}
				return removeUpdateFile(path)
			}
			manifest := testManifest(config.Channel, newPayload)
			manifest.Version = "new"
			manifest.WorkerProtocol = 7
			response := updater.Update(t.Context(), manifest, bytes.NewReader(newPayload))
			if response.OK || response.RolledBack != test.wantRolledBack ||
				!strings.Contains(response.Error, cleanupErr.Error()) {
				t.Fatalf("Update response = %#v", response)
			}
			if !fileExists(config.JournalPath()) {
				t.Fatal("failed cleanup unexpectedly removed update journal")
			}
		})
	}
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
		status: supervisor.WorkerStatus{
			Installed: true, Running: true, CoreReady: true, Version: "new", Protocol: 7,
		},
	}
	updater := NewUpdater(config, worker, os.Getuid())
	updater.verifyArtifact = func(context.Context, string, supervisor.UpdateManifest) error { return nil }
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

func TestUpdaterRecoverClearsJournalForHealthyWorker(t *testing.T) {
	config := testConfig(t)
	worker := &fakeWorker{config: config, status: supervisor.WorkerStatus{
		Installed: true, Running: true, CoreReady: true,
	}}
	updater := NewUpdater(config, worker, os.Getuid())
	if err := updater.writeJournal(journal{
		RequestID: "request-1", Phase: journalPhaseStaged,
		Version: "next", SHA256: strings.Repeat("a", 64),
	}); err != nil {
		t.Fatal(err)
	}
	staged := updater.stagedPath()
	writeExecutable(t, staged, machOBytes("staged"))

	if err := updater.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(config.JournalPath()); !os.IsNotExist(err) {
		t.Fatalf("recovery journal remains: %v", err)
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Fatalf("staged worker remains after recovery: %v", err)
	}
}

func TestUpdaterRecoverRollsBackMismatchedHealthyWorker(t *testing.T) {
	config := testConfig(t)
	currentPayload := machOBytes("unexpected")
	previousPayload := machOBytes("previous")
	writeExecutable(t, config.WorkerBinaryPath, currentPayload)
	writeExecutable(t, config.PreviousPath(), previousPayload)
	worker := &fakeWorker{config: config, status: supervisor.WorkerStatus{
		Installed: true, Running: true, CoreReady: true, Version: "unexpected",
	}}
	updater := NewUpdater(config, worker, os.Getuid())
	if err := updater.writeJournal(journal{
		RequestID: "request-1", Phase: journalPhaseSwapping,
		Version: "expected", SHA256: strings.Repeat("b", 64),
	}); err != nil {
		t.Fatal(err)
	}

	if err := updater.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertBytes(t, config.WorkerBinaryPath, previousPayload)
	if _, err := os.Stat(config.JournalPath()); !os.IsNotExist(err) {
		t.Fatalf("recovery journal remains: %v", err)
	}
}

func TestUpdaterManualRollbackRestoresPreviousWorker(t *testing.T) {
	config := testConfig(t)
	currentPayload := machOBytes("current")
	previousPayload := machOBytes("previous")
	writeExecutable(t, config.WorkerBinaryPath, currentPayload)
	writeExecutable(t, config.PreviousPath(), previousPayload)
	worker := &fakeWorker{config: config, status: supervisor.WorkerStatus{
		Installed: true, Running: true, CoreReady: true,
	}}
	updater := NewUpdater(config, worker, os.Getuid())

	response := updater.Rollback(t.Context())
	if !response.OK || !response.RolledBack || worker.stops != 1 || worker.starts != 1 {
		t.Fatalf("Rollback response=%#v stops=%d starts=%d", response, worker.stops, worker.starts)
	}
	assertBytes(t, config.WorkerBinaryPath, previousPayload)
}

func TestUpdaterRollbackStopFailurePreservesWorkerFiles(t *testing.T) {
	config := testConfig(t)
	currentPayload := machOBytes("current")
	previousPayload := machOBytes("previous")
	writeExecutable(t, config.WorkerBinaryPath, currentPayload)
	writeExecutable(t, config.PreviousPath(), previousPayload)
	stopErr := fmt.Errorf("stop failed")
	worker := &fakeWorker{
		config: config,
		status: supervisor.WorkerStatus{
			Installed: true, Running: true, CoreReady: true,
		},
		stopErrOnce: stopErr,
	}
	updater := NewUpdater(config, worker, os.Getuid())

	response := updater.Rollback(t.Context())
	if response.OK || response.RolledBack || !strings.Contains(response.Error, stopErr.Error()) {
		t.Fatalf("Rollback response = %#v", response)
	}
	assertBytes(t, config.WorkerBinaryPath, currentPayload)
	assertBytes(t, config.PreviousPath(), previousPayload)
}

func TestUpdaterRestartCyclesWorker(t *testing.T) {
	config := testConfig(t)
	worker := &fakeWorker{config: config, status: supervisor.WorkerStatus{
		Installed: true, Running: true, CoreReady: true,
	}}
	updater := NewUpdater(config, worker, os.Getuid())

	response := updater.Restart(t.Context())
	if !response.OK || worker.stops != 1 || worker.starts != 1 {
		t.Fatalf("Restart response=%#v stops=%d starts=%d", response, worker.stops, worker.starts)
	}
}

func TestUpdaterRestartWaitsForWorkerReadiness(t *testing.T) {
	config := testConfig(t)
	worker := &fakeWorker{
		config: config,
		status: supervisor.WorkerStatus{
			Installed: true, Running: true, CoreReady: true,
		},
		readyAfter: 3,
	}
	updater := NewUpdater(config, worker, os.Getuid())
	updater.readyTimeout = time.Second
	updater.readyInterval = time.Millisecond

	response := updater.Restart(t.Context())
	if !response.OK || worker.statusCalls < worker.readyAfter {
		t.Fatalf(
			"Restart response=%#v statusCalls=%d want at least %d",
			response,
			worker.statusCalls,
			worker.readyAfter,
		)
	}
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

func testManifest(channel string, payload []byte) supervisor.UpdateManifest {
	sum := sha256.Sum256(payload)
	return supervisor.UpdateManifest{
		SchemaVersion: supervisor.SchemaVersion, RequestID: "request-1",
		Channel: channel, Version: "dev", WorkerProtocol: 7,
		MinimumSupervisorProtocol: supervisor.Version,
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
