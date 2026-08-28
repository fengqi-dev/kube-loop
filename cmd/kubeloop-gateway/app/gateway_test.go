package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/pflag"

	"github.com/fengqi-dev/kube-loop/internal/buildinfo"
	options "github.com/fengqi-dev/kube-loop/internal/gateway/config"
)

var testBuildInfo = buildinfo.Info{Version: "1.2.3"}

func executeGateway(ctx context.Context, args []string, info buildinfo.Info, stdout, stderr io.Writer) int {
	command := newGatewayCommand(info)
	command.SetArgs(args)
	command.SetOut(stdout)
	command.SetErr(stderr)
	if err := command.ExecuteContext(ctx); err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	return 0
}

func TestGatewayVersionCommands(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--version"}} {
		var stdout, stderr bytes.Buffer
		if code := executeGateway(context.Background(), args, testBuildInfo, &stdout, &stderr); code != 0 {
			t.Fatalf("args=%v exit=%d stderr=%q", args, code, stderr.String())
		}
		if stdout.String() != "kubeloop-gateway "+testBuildInfo.Version+"\n" || stderr.Len() != 0 {
			t.Fatalf("args=%v stdout=%q stderr=%q", args, stdout.String(), stderr.String())
		}
	}
}

func TestGatewayInvalidFlagUsesUsageExitCode(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := executeGateway(context.Background(), []string{"--unknown"}, testBuildInfo, &stdout, &stderr); code != 1 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestGatewayConfigFlagOverridesEnvironment(t *testing.T) {
	t.Setenv("KUBELOOP_GATEWAY_CONFIG_FILE", "/env/config.yaml")
	config := options.NewConfigResolver()
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.String("config", "", "")
	if err := config.BindPFlag("gateway.config-file", flags.Lookup("config")); err != nil {
		t.Fatal(err)
	}
	if err := flags.Parse([]string{"--config", "/flag/config.yaml"}); err != nil {
		t.Fatal(err)
	}
	environment, err := options.LoadEnvironmentFrom(config)
	if err != nil {
		t.Fatal(err)
	}
	if environment.ConfigFile != "/flag/config.yaml" {
		t.Fatalf("Gateway config path = %q", environment.ConfigFile)
	}
}

func TestGatewayOverridesUseFlagEnvironmentAndFilePrecedence(t *testing.T) {
	loaded := options.Config{}
	loaded.HTTP.Listen = ":8080"
	loaded.HTTP.Path = "/v1/tunnel"
	loaded.WebSocket.MaxSessions = 256
	loaded.WebSocket.MaxSessionsPerUser = 8
	loaded.WebSocket.MaxStreamsPerSession = 128
	loaded.WebSocket.MaxFrameBytes = 1 << 20
	loaded.WebSocket.HandshakeTimeout.Duration = time.Second
	loaded.WebSocket.StreamIdleTimeout.Duration = time.Second
	loaded.DrainTimeout.Duration = time.Second
	loaded.Relay.ControlPlaneURL = "https://file.example.test"
	loaded.Relay.Endpoint = "wss://file.example.test/tunnel"
	loaded.Relay.ReplayEntries = 1
	t.Setenv("KUBELOOP_GATEWAY_LOG_LEVEL", "warn")
	config := options.NewConfigResolver()
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.String("listen", "", "")
	if err := config.BindPFlag("gateway.http.listen", flags.Lookup("listen")); err != nil {
		t.Fatal(err)
	}
	if err := flags.Parse([]string{"--listen", ":7443"}); err != nil {
		t.Fatal(err)
	}
	resolved, err := options.ApplyOverrides(config, loaded)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.HTTP.Listen != ":7443" || resolved.LogLevel != "warn" ||
		resolved.Relay.ControlPlaneURL != "https://file.example.test" {
		t.Fatalf("Gateway overrides = %#v", resolved)
	}
}

func TestLoadGatewayConfigAppliesDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kubeloop.yaml")
	raw := []byte(
		"controlPlane: {}\ngateway:\n  relay:\n    controlPlaneURL: https://registry.example.test\n    endpoint: wss://relay.example.test/tunnel\n",
	)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := options.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.HTTP.Listen != ":8080" || config.HTTP.Path != "/v1/tunnel" || config.WebSocket.MaxSessions != 256 {
		t.Fatalf("Gateway defaults = %#v", config)
	}
}

func TestLoadGatewayConfigRejectsLegacyRelayFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kubeloop.yaml")
	raw := []byte(
		"controlPlane: {}\ngateway:\n  relay:\n    controlPlaneURL: https://registry.example.test\n    endpoint: wss://relay.example.test/tunnel\n    id: legacy\n",
	)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := options.LoadConfig(path); err == nil {
		t.Fatal("legacy Relay ID was accepted")
	}
}

func TestLoadGatewayConfigRequiresUnifiedDocument(t *testing.T) {
	tests := map[string]string{
		"legacy root":     "relay:\n  controlPlaneURL: https://registry.example.test\n",
		"missing control": "gateway:\n  relay:\n    controlPlaneURL: https://registry.example.test\n",
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "kubeloop.yaml")
			if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := options.LoadConfig(path); err == nil {
				t.Fatal("non-unified configuration was accepted")
			}
		})
	}
}

func TestExpandRelayEndpointUsesDownwardAPIIdentity(t *testing.T) {
	endpoint, err := options.ExpandRelayEndpoint(
		"wss://{podName}.relay.example/tunnel/{podUID}",
		options.Environment{PodName: "gateway-7", PodUID: "44444444-4444-4444-8444-444444444444"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if endpoint != "wss://gateway-7.relay.example/tunnel/44444444-4444-4444-8444-444444444444" {
		t.Fatalf("endpoint = %q", endpoint)
	}
}

func TestLoadGatewayEnvironment(t *testing.T) {
	t.Setenv("KUBELOOP_GATEWAY_CONFIG_FILE", " /etc/kubeloop/gateway/kubeloop.yaml ")
	t.Setenv("KUBELOOP_POD_NAME", " gateway-7 ")
	t.Setenv("KUBELOOP_POD_UID", " pod-uid ")
	t.Setenv("KUBELOOP_POD_IP", " 10.0.0.7 ")

	environment, err := options.LoadEnvironmentFrom(options.NewConfigResolver())
	if err != nil {
		t.Fatal(err)
	}
	if environment.ConfigFile != "/etc/kubeloop/gateway/kubeloop.yaml" || environment.PodName != "gateway-7" ||
		environment.PodUID != "pod-uid" || environment.PodIP != "10.0.0.7" {
		t.Fatalf("Gateway environment = %#v", environment)
	}
}

func TestLoadGatewayEnvironmentRequiresConfigFile(t *testing.T) {
	t.Setenv("KUBELOOP_GATEWAY_CONFIG_FILE", "")

	if _, err := options.LoadEnvironmentFrom(options.NewConfigResolver()); err == nil {
		t.Fatal("missing Gateway configuration file was accepted")
	}
}
