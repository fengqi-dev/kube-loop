//go:build e2e

package harness

import (
	"context"
	"os"
	"testing"
	"time"
)

const (
	defaultContext = "minikube"
	defaultImage   = "kube-loop-gateway:dev"

	// EchoNamespace is the Minikube namespace for e2e echo fixtures.
	EchoNamespace = "kubeloop-e2e"
)

func RequireE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("KUBELOOP_E2E") != "1" {
		t.Skip("set KUBELOOP_E2E=1 to run Minikube end-to-end tests")
	}
}

func KubeContext() string {
	if value := os.Getenv("KUBELOOP_E2E_CONTEXT"); value != "" {
		return value
	}
	return defaultContext
}

func GatewayImage() string {
	if value := os.Getenv("KUBELOOP_GATEWAY_IMAGE"); value != "" {
		return value
	}
	return defaultImage
}

func TestContext(t *testing.T, timeout time.Duration) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), timeout)
}
