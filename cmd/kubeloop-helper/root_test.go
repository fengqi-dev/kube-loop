package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRootCommandVersion(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{"version"}, {"--version"}} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			t.Parallel()
			stdout, stderr, exitCode := executeForTest(t, args, commandDependencies{}, "1.2.3")
			if exitCode != 0 {
				t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr)
			}
			if stdout != "1.2.3\n" {
				t.Fatalf("stdout = %q", stdout)
			}
			if stderr != "" {
				t.Fatalf("stderr = %q", stderr)
			}
		})
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
		"install",
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

func TestCommandValidationUsesUsageExitCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
	}{
		{name: "missing install flags", args: []string{"install"}},
		{name: "negative uid", args: []string{"install", "--source", "helper", "--token", "secret", "--home", "/tmp", "--uid", "-1"}},
		{name: "extra run argument", args: []string{"run", "extra"}},
		{name: "unknown flag", args: []string{"version", "--unknown"}},
		{name: "unknown command", args: []string{"unknown"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, stderr, exitCode := executeForTest(t, test.args, commandDependencies{}, "dev")
			if exitCode != 2 {
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
	if exitCode := execute(ctx, []string{"run"}, &stdout, &stderr, dependencies, "dev"); exitCode != 0 {
		t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr.String())
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
	if strings.Contains(stdout, "elevated") {
		t.Fatalf("internal elevated command appeared in help:\n%s", stdout)
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
	exitCode := execute(t.Context(), args, &stdout, &stderr, dependencies, commandVersion)
	return stdout.String(), stderr.String(), exitCode
}
