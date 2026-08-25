package main

import (
	"bytes"
	"context"
	"log"
	"log/slog"
	"strings"
	"testing"
)

func TestTUIVersionCommands(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--version"}} {
		var stdout, stderr bytes.Buffer
		if code := executeTUI(context.Background(), args, &stdout, &stderr); code != 0 {
			t.Fatalf("args=%v exit=%d stderr=%q", args, code, stderr.String())
		}
		if stdout.String() != "kubeloop "+version+"\n" || stderr.Len() != 0 {
			t.Fatalf("args=%v stdout=%q stderr=%q", args, stdout.String(), stderr.String())
		}
	}
}

func TestTUIInvalidArgumentsUseUsageExitCode(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := executeTUI(context.Background(), []string{"unexpected"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestSilenceTUIProcessLogsRestoresDefaults(t *testing.T) {
	previousLogger := slog.Default()
	previousLogWriter := log.Writer()
	t.Cleanup(func() {
		slog.SetDefault(previousLogger)
		log.SetOutput(previousLogWriter)
	})

	var output bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
	log.SetOutput(&output)
	restore := silenceTUIProcessLogs()
	slog.Info("hidden structured output")
	log.Print("hidden standard output")
	restore()
	slog.Info("visible structured output")
	log.Print("visible standard output")

	got := output.String()
	if strings.Contains(got, "hidden") {
		t.Fatalf("TUI process logs escaped to the terminal: %q", got)
	}
	if !strings.Contains(got, "visible structured output") ||
		!strings.Contains(got, "visible standard output") {
		t.Fatalf("process log defaults were not restored: %q", got)
	}
}
