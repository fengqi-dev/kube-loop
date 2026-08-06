package app

import (
	"testing"
	"testing/fstest"
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
