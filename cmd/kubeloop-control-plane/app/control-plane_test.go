package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/pflag"

	"github.com/fengqi-dev/kube-loop/internal/buildinfo"
	options "github.com/fengqi-dev/kube-loop/internal/controlplane/config"
)

var testBuildInfo = buildinfo.Info{Version: "1.2.3", Commit: "abc123", BuildDate: "2026-08-27"}
var testProtocol = buildinfo.ProtocolRange{Min: "2.0", Max: "2.0"}

func executeControlPlane(
	ctx context.Context,
	args []string,
	info buildinfo.Info,
	protocol buildinfo.ProtocolRange,
	stdout, stderr io.Writer,
) int {
	command := newControlPlaneCommand(info, protocol)
	command.SetArgs(args)
	command.SetOut(stdout)
	command.SetErr(stderr)
	if err := command.ExecuteContext(ctx); err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	return 0
}

func TestControlPlaneVersionCommands(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--version"}} {
		var stdout, stderr bytes.Buffer
		if code := executeControlPlane(
			context.Background(),
			args,
			testBuildInfo,
			testProtocol,
			&stdout,
			&stderr,
		); code != 0 {
			t.Fatalf("args=%v exit=%d stderr=%q", args, code, stderr.String())
		}
		if stdout.String() != "kubeloop-control-plane "+testBuildInfo.Version+"\n" || stderr.Len() != 0 {
			t.Fatalf("args=%v stdout=%q stderr=%q", args, stdout.String(), stderr.String())
		}
	}
}

func TestControlPlaneInvalidFlagUsesUsageExitCode(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := executeControlPlane(
		context.Background(),
		[]string{"--unknown"},
		testBuildInfo,
		testProtocol,
		&stdout,
		&stderr,
	); code != 1 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestControlPlaneConfigFlagOverridesEnvironment(t *testing.T) {
	t.Setenv("KUBELOOP_CONTROL_PLANE_CONFIG_FILE", "/env/config.yaml")
	config := options.NewConfigResolver()
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.String("config", "", "")
	if err := config.BindPFlag("control-plane.config-file", flags.Lookup("config")); err != nil {
		t.Fatal(err)
	}
	if err := flags.Parse([]string{"--config", "/flag/config.yaml"}); err != nil {
		t.Fatal(err)
	}
	if got := options.ConfigPath(config); got != "/flag/config.yaml" {
		t.Fatalf("config path = %q", got)
	}
}

func TestControlPlaneOverridesUseFlagEnvironmentAndFilePrecedence(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "kubeloop.yaml")
	raw := `
controlPlane:
  api:
    listen: :8080
    publicURL: https://file.example.test
  authentication:
    oauth:
      oidcSigningKeyFile: /run/secrets/oidc.pem
      hmacSecretFile: /run/secrets/hmac
  relay:
    ticket:
      signingKeyFile: /run/secrets/relay.pem
    registry:
      certificateFile: /run/secrets/tls.crt
      privateKeyFile: /run/secrets/tls.key
      namespace: kubeloop
      serviceAccount: gateway
  logging:
    level: info
gateway: {}
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := options.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBELOOP_CONTROL_PLANE_LOGGING_LEVEL", "debug")
	config := options.NewConfigResolver()
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.String("public-url", "", "")
	if err := config.BindPFlag("control-plane.api.public-url", flags.Lookup("public-url")); err != nil {
		t.Fatal(err)
	}
	if err := flags.Parse([]string{"--public-url", "https://flag.example.test"}); err != nil {
		t.Fatal(err)
	}
	loaded, err = options.ApplyOverrides(config, loaded)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Document.API.PublicURL != "https://flag.example.test" ||
		loaded.Document.Logging.Level != "debug" || loaded.Document.API.Listen != ":8080" {
		t.Fatalf("Control Plane overrides = %#v", loaded.Document)
	}
}
