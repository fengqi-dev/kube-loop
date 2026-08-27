package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/buildinfo"
)

func TestHelperCommandVersion(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{versionCommandName}, {"--version"}} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			t.Parallel()
			stdout, stderr, exitCode := executeForTest(t, args, commandDependencies{}, "1.2.3")
			if exitCode != 0 {
				t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr)
			}
			if stdout != "kubeloop-helper 1.2.3\n" {
				t.Fatalf("stdout = %q", stdout)
			}
			if stderr != "" {
				t.Fatalf("stderr = %q", stderr)
			}
		})
	}
}

func TestIdentityCommand(t *testing.T) {
	t.Parallel()
	stdout, stderr, exitCode := executeForTest(t, []string{"identity"}, commandDependencies{}, "1.2.3")
	if exitCode != 0 || stderr != "" {
		t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr)
	}
	want := `{"kind":"kubeloop-helper","version":"1.2.3","protocol":6}` + "\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
}

func TestInstallCommand(t *testing.T) {
	t.Parallel()

	var received installOptions
	dependencies := commandDependencies{
		install: func(options installOptions) error {
			received = options
			return nil
		},
	}
	stdout, stderr, exitCode := executeForTest(t, []string{
		installCommandName,
		"--source", "/tmp/helper",
		"--token", "secret",
		"--uid", "1000",
		"--version", "1.2.3",
		"--home", "/home/user",
		"--sid", "owner",
		"--sing-box", "/tmp/sing-box",
	}, dependencies, "dev")
	if exitCode != 0 {
		t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr)
	}
	if stdout != "" || stderr != "" {
		t.Fatalf("stdout/stderr = %q/%q", stdout, stderr)
	}
	if received != (installOptions{
		source: "/tmp/helper", token: "secret", uid: 1000, version: "1.2.3",
		home: "/home/user", ownerSID: "owner", singBox: "/tmp/sing-box",
	}) {
		t.Fatalf("install options = %#v", received)
	}
}

func TestCommandValidationUsesFailureExitCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
	}{
		{name: "missing install flags", args: []string{installCommandName}},
		{
			name: "negative uid",
			args: []string{
				installCommandName,
				"--source",
				"helper",
				"--token",
				"secret",
				"--home",
				"/tmp",
				"--uid",
				"-1",
			},
		},
		{name: "extra run argument", args: []string{runCommandName, "extra"}},
		{name: "unknown flag", args: []string{versionCommandName, "--unknown"}},
		{name: "unknown command", args: []string{"unknown"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, stderr, exitCode := executeForTest(t, test.args, commandDependencies{}, "dev")
			if exitCode != 1 {
				t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr)
			}
			if !strings.HasPrefix(stderr, "Error: ") {
				t.Fatalf("stderr = %q", stderr)
			}
		})
	}
}

func TestRunCommandPropagatesContext(t *testing.T) {
	t.Parallel()

	type contextKey struct{}
	ctx := context.WithValue(t.Context(), contextKey{}, "request")
	called := false
	dependencies := commandDependencies{
		run: func(runContext context.Context) error {
			called = true
			if runContext.Value(contextKey{}) != "request" {
				t.Fatal("run context was not propagated")
			}
			return nil
		},
	}
	var stdout, stderr bytes.Buffer
	command := newHelperCommand(dependencies, buildinfo.Info{Version: "dev"})
	command.SetArgs([]string{runCommandName})
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	if err := command.ExecuteContext(ctx); err != nil {
		t.Fatalf("execute command: %v, stderr = %q", err, stderr.String())
	}
	if !called {
		t.Fatal("run dependency was not called")
	}
}

func TestRuntimeErrorUsesGeneralExitCode(t *testing.T) {
	t.Parallel()

	dependencies := commandDependencies{
		uninstall: func() error { return errors.New("remove service") },
	}
	stdout, stderr, exitCode := executeForTest(t, []string{"uninstall"}, dependencies, "dev")
	if exitCode != 1 {
		t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr)
	}
	if stdout != "" || stderr != "Error: remove service\n" {
		t.Fatalf("stdout/stderr = %q/%q", stdout, stderr)
	}
}

func TestElevatedCommandIsHidden(t *testing.T) {
	t.Parallel()

	stdout, stderr, exitCode := executeForTest(t, []string{"--help"}, commandDependencies{}, "dev")
	if exitCode != 0 {
		t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr)
	}
	if strings.Contains(stdout, "elevated") || strings.Contains(stdout, trustCertificateCommandName) {
		t.Fatalf("internal elevated command appeared in help:\n%s", stdout)
	}
}

func TestTrustCertificateCommand(t *testing.T) {
	t.Parallel()

	var received trustCertificateOptions
	dependencies := commandDependencies{
		trustCertificate: func(_ context.Context, options trustCertificateOptions) error {
			received = options
			return nil
		},
	}
	stdout, stderr, exitCode := executeForTest(t, []string{
		trustCertificateCommandName,
		"--operation", "install",
		"--certificate", "/tmp/inspection-ca.pem",
	}, dependencies, "dev")
	if exitCode != 0 || stdout != "" || stderr != "" {
		t.Fatalf("exit code = %d, stdout/stderr = %q/%q", exitCode, stdout, stderr)
	}
	if received != (trustCertificateOptions{
		operation: "install", certificate: "/tmp/inspection-ca.pem",
	}) {
		t.Fatalf("trust certificate options = %#v", received)
	}
}

func executeForTest(
	t *testing.T,
	args []string,
	dependencies commandDependencies,
	commandVersion string,
) (string, string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	command := newHelperCommand(dependencies, buildinfo.Info{Version: commandVersion})
	command.SetArgs(args)
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	exitCode := 0
	if err := command.ExecuteContext(t.Context()); err != nil {
		_, _ = fmt.Fprintf(&stderr, "Error: %v\n", err)
		exitCode = 1
	}
	return stdout.String(), stderr.String(), exitCode
}
