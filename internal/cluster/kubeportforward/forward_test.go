package kubeportforward

import (
	"context"
	"strings"
	"testing"

	"k8s.io/client-go/rest"
)

func TestStartValidatesRequest(t *testing.T) {
	tests := []struct {
		name       string
		config     *rest.Config
		namespace  string
		pod        string
		remotePort uint16
		want       string
	}{
		{name: "config", want: "REST config is required"},
		{name: "namespace", config: &rest.Config{}, pod: "pod", remotePort: 80, want: "namespace is required"},
		{name: "pod", config: &rest.Config{}, namespace: "default", remotePort: 80, want: "pod name is required"},
		{name: "port", config: &rest.Config{}, namespace: "default", pod: "pod", want: "remote port is required"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Start(
				context.Background(),
				test.config,
				test.namespace,
				test.pod,
				0,
				test.remotePort,
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Start() error = %v, want %q", err, test.want)
			}
		})
	}
}
