//go:build darwin

package main

import (
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/helper"
	"github.com/fengqi-dev/kube-loop/internal/supervisor"
)

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
