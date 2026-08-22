//go:build darwin

package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/helper"
	"github.com/fengqi-dev/kube-loop/internal/supervisor"
)

func TestSupervisorVersionCommands(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--version"}} {
		var stdout, stderr bytes.Buffer
		if code := executeSupervisor(context.Background(), args, &stdout, &stderr); code != 0 {
			t.Fatalf("args=%v exit=%d stderr=%q", args, code, stderr.String())
		}
		if stdout.String() != "kubeloop-supervisor "+version+"\n" || stderr.Len() != 0 {
			t.Fatalf("args=%v stdout=%q stderr=%q", args, stdout.String(), stderr.String())
		}
	}
}

func TestSupervisorInvalidCommandUsesUsageExitCode(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := executeSupervisor(context.Background(), []string{"unknown"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestConfigureChannelUsesWorkerChannelInsteadOfSupervisorBinaryVersion(t *testing.T) {
	originalHelperVersion := helper.Version
	originalSupervisorVersion := supervisor.Version
	t.Cleanup(func() {
		helper.Version = originalHelperVersion
		supervisor.Version = originalSupervisorVersion
	})

	if err := configureChannel("dev", "dev"); err != nil {
		t.Fatal(err)
	}
	if helper.Version != "dev" || supervisor.CurrentConfig().Channel != "dev" {
		t.Fatalf("dev channel selected helper=%q supervisor=%q", helper.Version, supervisor.CurrentConfig().Channel)
	}

	if err := configureChannel("release", "v2.1.1"); err != nil {
		t.Fatal(err)
	}
	if helper.Version != "v2.1.1" || supervisor.CurrentConfig().Channel != "release" {
		t.Fatalf("release channel selected helper=%q supervisor=%q", helper.Version, supervisor.CurrentConfig().Channel)
	}
}

func TestConfigureChannelRejectsWorkerChannelMismatch(t *testing.T) {
	if err := configureChannel("dev", "v2.1.1"); err == nil {
		t.Fatal("dev channel accepted release worker")
	}
	if err := configureChannel("release", "dev"); err == nil {
		t.Fatal("release channel accepted dev worker")
	}
}
