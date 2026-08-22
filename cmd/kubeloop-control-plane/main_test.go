package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/pflag"
)

func TestControlPlaneVersionCommands(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--version"}} {
		var stdout, stderr bytes.Buffer
		if code := executeControlPlane(context.Background(), args, &stdout, &stderr); code != 0 {
			t.Fatalf("args=%v exit=%d stderr=%q", args, code, stderr.String())
		}
		if stdout.String() != "kubeloop-control-plane "+version+"\n" || stderr.Len() != 0 {
			t.Fatalf("args=%v stdout=%q stderr=%q", args, stdout.String(), stderr.String())
		}
	}
}

func TestControlPlaneInvalidFlagUsesUsageExitCode(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := executeControlPlane(context.Background(), []string{"--unknown"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestControlPlaneConfigFlagOverridesEnvironment(t *testing.T) {
	t.Setenv("KUBELOOP_CONTROL_PLANE_CONFIG_FILE", "/env/config.yaml")
	config := newControlPlaneConfigResolver()
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.String("config", "", "")
	if err := config.BindPFlag("control-plane.config-file", flags.Lookup("config")); err != nil {
		t.Fatal(err)
	}
	if err := flags.Parse([]string{"--config", "/flag/config.yaml"}); err != nil {
		t.Fatal(err)
	}
	if got := controlPlaneOptionsFrom(config); got != "/flag/config.yaml" {
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
	loaded, err := loadControlPlaneConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBELOOP_CONTROL_PLANE_LOGGING_LEVEL", "debug")
	config := newControlPlaneConfigResolver()
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.String("public-url", "", "")
	if err := config.BindPFlag("control-plane.api.public-url", flags.Lookup("public-url")); err != nil {
		t.Fatal(err)
	}
	if err := flags.Parse([]string{"--public-url", "https://flag.example.test"}); err != nil {
		t.Fatal(err)
	}
	loaded, err = applyControlPlaneOverrides(config, loaded)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Document.API.PublicURL != "https://flag.example.test" ||
		loaded.Document.Logging.Level != "debug" || loaded.Document.API.Listen != ":8080" {
		t.Fatalf("Control Plane overrides = %#v", loaded.Document)
	}
}
