package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"

	"github.com/fengqi-dev/kube-loop/internal/buildinfo"
	operatorruntime "github.com/fengqi-dev/kube-loop/internal/operator/runtime"
)

var testBuildInfo = buildinfo.Info{Version: "1.2.3"}

func executeCommand(ctx context.Context, command *cobra.Command, args []string, stdout, stderr io.Writer) int {
	command.SetArgs(args)
	command.SetOut(stdout)
	command.SetErr(stderr)
	if err := command.ExecuteContext(ctx); err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	return 0
}

func executeOperator(ctx context.Context, args []string, info buildinfo.Info, stdout, stderr io.Writer) int {
	return executeCommand(ctx, newOperatorCommandWithConfig(newOperatorConfigResolver(), info), args, stdout, stderr)
}

func TestOperatorVersionCommands(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--version"}} {
		var stdout, stderr bytes.Buffer
		if code := executeOperator(context.Background(), args, testBuildInfo, &stdout, &stderr); code != 0 {
			t.Fatalf("args=%v exit=%d stderr=%q", args, code, stderr.String())
		}
		if stdout.String() != "kubeloop-operator "+testBuildInfo.Version+"\n" || stderr.Len() != 0 {
			t.Fatalf("args=%v stdout=%q stderr=%q", args, stdout.String(), stderr.String())
		}
	}
}

func TestOperatorInvalidFlagUsesUsageExitCode(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := executeOperator(
		context.Background(),
		[]string{"--unknown"},
		testBuildInfo,
		&stdout,
		&stderr,
	); code != 1 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestOperatorCommandDefaults(t *testing.T) {
	config := newOperatorConfigResolver()
	_ = newOperatorCommandWithConfig(config, testBuildInfo)
	options := operatorOptionsFrom(config)
	if options.MetricsAddress != "0" || options.ProbeAddress != ":8081" || !options.SecureMetrics {
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
	if options.MetricsAddress != ":9443" || !options.LeaderElection || options.SecureMetrics {
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
	command := newOperatorCommandWithConfig(config, testBuildInfo)
	var got operatorruntime.Options
	command.RunE = func(*cobra.Command, []string) error {
		got = operatorOptionsFrom(config)
		return nil
	}
	var stdout, stderr bytes.Buffer
	code := executeCommand(
		context.Background(), command,
		[]string{
			"--config", configFile,
			"--crd-file", "/etc/kubeloop/crd.yaml",
			"--metrics-bind-address", ":7443",
		},
		&stdout, &stderr,
	)
	if code != 0 || got.CRDFile != "/etc/kubeloop/crd.yaml" ||
		got.MetricsAddress != ":7443" || !got.LeaderElection {
		t.Fatalf("exit=%d options=%#v stderr=%q", code, got, stderr.String())
	}
}
