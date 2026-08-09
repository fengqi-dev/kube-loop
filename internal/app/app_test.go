package app

import (
	"testing"
	"testing/fstest"

	"github.com/fengqi-dev/kube-loop/internal/cluster"
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
