//go:build darwin

package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/buildinfo"
	"github.com/fengqi-dev/kube-loop/internal/helper"
	"github.com/fengqi-dev/kube-loop/internal/supervisor"
	supervisorapp "github.com/fengqi-dev/kube-loop/internal/supervisorapp"
)

var testBuildInfo = buildinfo.Info{Version: "1.2.3"}

func executeSupervisor(ctx context.Context, args []string, info buildinfo.Info, stdout, stderr io.Writer) int {
	command := newSupervisorCommand(info)
	command.SetArgs(args)
	command.SetOut(stdout)
	command.SetErr(stderr)
	if err := command.ExecuteContext(ctx); err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	return 0
}

func TestSupervisorVersionCommands(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--version"}} {
		var stdout, stderr bytes.Buffer
		if code := executeSupervisor(context.Background(), args, testBuildInfo, &stdout, &stderr); code != 0 {
			t.Fatalf("args=%v exit=%d stderr=%q", args, code, stderr.String())
		}
		if stdout.String() != "kubeloop-supervisor "+testBuildInfo.Version+"\n" || stderr.Len() != 0 {
			t.Fatalf("args=%v stdout=%q stderr=%q", args, stdout.String(), stderr.String())
		}
	}
}

func TestSupervisorInvalidCommandUsesUsageExitCode(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := executeSupervisor(
		context.Background(),
		[]string{"unknown"},
		testBuildInfo,
		&stdout,
		&stderr,
	); code != 1 {
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

	if err := supervisorapp.ConfigureChannel(supervisorapp.ChannelDev, supervisorapp.ChannelDev); err != nil {
		t.Fatal(err)
	}
	if helper.Version != "dev" || supervisor.CurrentConfig().Channel != "dev" {
		t.Fatalf("dev channel selected helper=%q supervisor=%q", helper.Version, supervisor.CurrentConfig().Channel)
	}

	if err := supervisorapp.ConfigureChannel(supervisorapp.ChannelRelease, "v2.1.1"); err != nil {
		t.Fatal(err)
	}
	if helper.Version != "v2.1.1" || supervisor.CurrentConfig().Channel != "release" {
		t.Fatalf("release channel selected helper=%q supervisor=%q", helper.Version, supervisor.CurrentConfig().Channel)
	}
}

func TestConfigureChannelRejectsWorkerChannelMismatch(t *testing.T) {
	if err := supervisorapp.ConfigureChannel(supervisorapp.ChannelDev, "v2.1.1"); err == nil {
		t.Fatal("dev channel accepted release worker")
	}
	if err := supervisorapp.ConfigureChannel(supervisorapp.ChannelRelease, supervisorapp.ChannelDev); err == nil {
		t.Fatal("release channel accepted dev worker")
	}
}
