package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/fengqi-dev/kube-loop/internal/cluster"
	"github.com/fengqi-dev/kube-loop/internal/session"
	"github.com/fengqi-dev/kube-loop/internal/store"
)

func TestDevelopmentGatewayImage(t *testing.T) {
	files := fstest.MapFS{
		"build/embedded/gateway-image": {
			Data: []byte("kube-loop-gateway:dev-deadbeef\n"),
		},
	}
	if got := developmentGatewayImage(files); got != "kube-loop-gateway:dev-deadbeef" {
		t.Fatalf("development Gateway image = %q", got)
	}
	if got := developmentGatewayImage(nil); got != "" {
		t.Fatalf("nil filesystem Gateway image = %q", got)
	}
}

func TestGatewayResourceUsesConfiguredNamespace(t *testing.T) {
	namespace, name := gatewayResource(true, "abcd", "platform-networking")
	if namespace != "platform-networking" || name != cluster.GatewayName {
		t.Fatalf("shared resource = %s/%s", namespace, name)
	}
	namespace, name = gatewayResource(false, "abcd", "platform-networking")
	if namespace != "platform-networking" || name != "kubeloop-gateway-abcd" {
		t.Fatalf("private resource = %s/%s", namespace, name)
	}
	namespace, _ = gatewayResource(true, "", "")
	if namespace != cluster.GatewayNamespace {
		t.Fatalf("default namespace = %s", namespace)
	}
}

func TestAddKubeconfigContentStoresAndRemovesManagedFile(t *testing.T) {
	dir := t.TempDir()
	stateStore, err := store.Open(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	provider := cluster.NewProvider()
	manager := session.NewManager(provider, session.WithStore(stateStore))
	app := &App{provider: provider, manager: manager, store: stateStore}

	inventory, err := app.AddKubeconfigContent(`apiVersion: v1
kind: Config
contexts:
- name: pasted
  context:
    cluster: demo
clusters:
- name: demo
  cluster:
    server: https://example.test
`)
	if err != nil {
		t.Fatalf("AddKubeconfigContent: %v", err)
	}
	var imported string
	for _, file := range inventory.Files {
		if strings.HasPrefix(filepath.Base(file.Path), "clipboard-") {
			imported = file.Path
			break
		}
	}
	if imported == "" {
		t.Fatalf("managed clipboard source missing: %+v", inventory.Files)
	}
	info, err := os.Stat(imported)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("clipboard kubeconfig mode = %o", info.Mode().Perm())
	}
	if _, err := app.RemoveKubeconfig(imported); err != nil {
		t.Fatalf("RemoveKubeconfig: %v", err)
	}
	if _, err := os.Stat(imported); !os.IsNotExist(err) {
		t.Fatalf("managed clipboard source was not removed: %v", err)
	}
}
