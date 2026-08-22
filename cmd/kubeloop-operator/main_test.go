package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"

	internalcli "github.com/fengqi-dev/kube-loop/internal/cli"
)

func TestOperatorVersionCommands(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--version"}} {
		var stdout, stderr bytes.Buffer
		if code := executeOperator(context.Background(), args, &stdout, &stderr); code != 0 {
			t.Fatalf("args=%v exit=%d stderr=%q", args, code, stderr.String())
		}
		if stdout.String() != "kubeloop-operator "+version+"\n" || stderr.Len() != 0 {
			t.Fatalf("args=%v stdout=%q stderr=%q", args, stdout.String(), stderr.String())
		}
	}
}

func TestOperatorInvalidFlagUsesUsageExitCode(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := executeOperator(context.Background(), []string{"--unknown"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestOperatorCommandDefaults(t *testing.T) {
	config := newOperatorConfigResolver()
	_ = newOperatorCommandWithConfig(config)
	options := operatorOptionsFrom(config)
	if options.metricsAddress != "0" || options.probeAddress != ":8081" || !options.secureMetrics {
		t.Fatalf("operator defaults = %#v", options)
	}
}

func TestOperatorViperReadsEnvironmentAndFile(t *testing.T) {
	t.Setenv("KUBELOOP_OPERATOR_METRICS_BIND_ADDRESS", ":9443")
	configFile := filepath.Join(t.TempDir(), "operator.yaml")
	if err := os.WriteFile(
		configFile,
		[]byte("operator:\n  leader-elect: true\n  metrics-secure: false\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	config := newOperatorConfigResolver()
	config.SetConfigFile(configFile)
	if err := config.ReadInConfig(); err != nil {
		t.Fatal(err)
	}
	options := operatorOptionsFrom(config)
	if options.metricsAddress != ":9443" || !options.leaderElection || options.secureMetrics {
		t.Fatalf("operator options = %#v", options)
	}
}

func TestOperatorFlagOverridesEnvironmentAndFile(t *testing.T) {
	t.Setenv("KUBELOOP_OPERATOR_METRICS_BIND_ADDRESS", ":9443")
	configFile := filepath.Join(t.TempDir(), "operator.yaml")
	if err := os.WriteFile(
		configFile,
		[]byte("operator:\n  metrics-bind-address: ':8443'\n  leader-elect: true\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	config := newOperatorConfigResolver()
	command := newOperatorCommandWithConfig(config)
	var got operatorOptions
	command.RunE = func(*cobra.Command, []string) error {
		got = operatorOptionsFrom(config)
		return nil
	}
	var stdout, stderr bytes.Buffer
	code := internalcli.Execute(
		context.Background(), command,
		[]string{"--config", configFile, "--metrics-bind-address", ":7443"},
		&stdout, &stderr,
	)
	if code != 0 || got.metricsAddress != ":7443" || !got.leaderElection {
		t.Fatalf("exit=%d options=%#v stderr=%q", code, got, stderr.String())
	}
}
