//go:build e2e

package harness

import (
	"context"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/fengqi-dev/kube-loop/internal/cluster"
	"github.com/fengqi-dev/kube-loop/internal/intercept"
	"github.com/fengqi-dev/kube-loop/internal/tunnel"
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

func NewProvider(t *testing.T) *cluster.Provider {
	t.Helper()
	return cluster.NewProvider()
}

func KubeClient(t *testing.T, provider *cluster.Provider) kubernetes.Interface {
	t.Helper()
	cfg, err := provider.RESTConfig(KubeContext())
	if err != nil {
		t.Fatal(err)
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestContext(t *testing.T, timeout time.Duration) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), timeout)
}

func InterceptListenPort(
	t *testing.T,
	ports []intercept.InterceptPort,
	servicePort int32,
	protocol corev1.Protocol,
) int {
	t.Helper()
	for _, port := range ports {
		if port.ServicePort == servicePort && port.Protocol == string(protocol) {
			return int(port.ListenPort)
		}
	}
	t.Fatalf("intercept listen port not found for %d/%s", servicePort, protocol)
	return 0
}

func EnsureGateway(
	t *testing.T, ctx context.Context, provider *cluster.Provider,
) (cluster.GatewayInfo, cluster.PortForward) {
	t.Helper()
	gateway, err := provider.EnsureGateway(ctx, KubeContext(), GatewayImage())
	if err != nil {
		t.Fatalf("ensure gateway: %v", err)
	}
	forwarder, err := provider.StartPortForward(ctx, KubeContext(), gateway.Name, cluster.GatewayPort)
	if err != nil {
		t.Fatalf("gateway port-forward: %v", err)
	}
	t.Cleanup(func() {
		if err := forwarder.Close(); err != nil {
			t.Logf("close gateway port-forward: %v", err)
		}
	})
	if err := waitGatewayControl(ctx, forwarder.Address()); err != nil {
		t.Fatalf("gateway control not ready: %v", err)
	}
	return gateway, forwarder
}

func waitGatewayControl(ctx context.Context, address string) error {
	deadline := time.Now().Add(60 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		var dialer net.Dialer
		conn, err := dialer.DialContext(ctx, "tcp", address)
		if err != nil {
			last = err
			time.Sleep(300 * time.Millisecond)
			continue
		}
		err = tunnel.WriteControlSession(conn)
		if err == nil {
			err = tunnel.ReadStatus(conn)
		}
		_ = conn.Close()
		if err == nil {
			return nil
		}
		last = err
		time.Sleep(300 * time.Millisecond)
	}
	if last == nil {
		last = fmt.Errorf("timed out")
	}
	return fmt.Errorf("control handshake at %s: %w", address, last)
}
