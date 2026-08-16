//go:build e2e

package harness

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/helper"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func StopAllHelperSessions() {
	client, err := helper.NewClient()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, _ = client.StopAll(ctx)
}

func EchoServiceIP(t *testing.T, ctx context.Context, client kubernetes.Interface) string {
	t.Helper()
	service, err := client.CoreV1().Services(EchoNamespace).Get(ctx, "echo", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if service.Spec.ClusterIP == "" {
		t.Fatal("echo service has no ClusterIP")
	}
	return service.Spec.ClusterIP
}

// AssertClusterDNSGone checks split-DNS no longer answers the cluster FQDN.
func AssertClusterDNSGone(t *testing.T, fqdn, clusterIP string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		ips, err := net.LookupHost(fqdn)
		if err != nil || !ContainsIP(ips, clusterIP) {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("cluster DNS %s still resolves to %s after disconnect", fqdn, clusterIP)
}

func ContainsIP(ips []string, want string) bool {
	for _, ip := range ips {
		if ip == want {
			return true
		}
	}
	return false
}
