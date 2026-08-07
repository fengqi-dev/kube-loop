//go:build e2e

package dns

import (
	"context"
	"testing"
	"time"

	"github.com/miekg/dns"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/fengqi-dev/kube-loop/e2e/harness"
	"github.com/fengqi-dev/kube-loop/internal/session"
	"github.com/fengqi-dev/kube-loop/internal/store"
)

func TestMain(m *testing.M) { harness.RunMain(m) }

func TestTUNDNSResolution(t *testing.T) {
	harness.RequireE2E(t)
	ctx, cancel := harness.TestContext(t, 2*time.Minute)
	defer cancel()

	provider := harness.NewProvider(t)
	client := harness.KubeClient(t, provider)
	if err := harness.EnsureEchoWorkload(ctx, client); err != nil {
		t.Fatal(err)
	}
	clusterIP := harness.EchoServiceIP(t, ctx, client)
	aliasDomain := "echo.kubeloop-e2e.test"
	priorityService := "kubeloop-e2e-dns-priority"
	echoNamespaceIP := ensureDNSService(t, ctx, client, harness.EchoNamespace, priorityService)
	defaultNamespaceIP := ensureDNSService(t, ctx, client, "default", priorityService)

	live := harness.ConnectSession(t, ctx, session.Request{
		Context: harness.KubeContext(), Namespace: harness.EchoNamespace,
	}, func(manager *session.Manager) {
		if err := manager.SetHostAliases(harness.KubeContext(), []store.HostAliasSpec{
			{Domain: aliasDomain, IP: clusterIP},
		}); err != nil {
			t.Fatal(err)
		}
	})

	port, err := live.Manager.DNSPort()
	if err != nil {
		t.Fatal(err)
	}

	fqdn := "echo." + harness.EchoNamespace + ".svc.cluster.local"
	svcForm := "echo." + harness.EchoNamespace + ".svc"
	nsForm := "echo." + harness.EchoNamespace

	t.Run("fqdn-a-udp", func(t *testing.T) {
		harness.WaitDNSA(t, port, fqdn, clusterIP)
	})
	t.Run("svc-relative-a", func(t *testing.T) {
		harness.WaitDNSA(t, port, svcForm, clusterIP)
	})
	t.Run("ns-relative-a", func(t *testing.T) {
		harness.WaitDNSA(t, port, nsForm, clusterIP)
	})
	t.Run("short-name-a", func(t *testing.T) {
		harness.WaitDNSA(t, port, "echo", clusterIP)
	})
	t.Run("host-alias-a", func(t *testing.T) {
		harness.WaitDNSA(t, port, aliasDomain, clusterIP)
	})
	t.Run("fqdn-a-tcp", func(t *testing.T) {
		harness.WaitDNSTCPA(t, port, fqdn, clusterIP)
	})
	t.Run("short-name-a-tcp", func(t *testing.T) {
		harness.WaitDNSTCPA(t, port, "echo", clusterIP)
	})
	t.Run("kubernetes-api-a", func(t *testing.T) {
		apiService, err := client.CoreV1().Services("default").Get(ctx, "kubernetes", metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		harness.WaitDNSA(t, port, "kubernetes.default.svc.cluster.local", apiService.Spec.ClusterIP)
		harness.WaitDNSA(t, port, "kubernetes.default.svc", apiService.Spec.ClusterIP)
	})
	t.Run("nxdomain-missing-service", func(t *testing.T) {
		harness.WaitDNSNXDOMAIN(t, port, "no-such-service."+harness.EchoNamespace+".svc.cluster.local")
	})
	t.Run("os-resolver-fqdn", func(t *testing.T) {
		harness.WaitLookupIP(t, fqdn, clusterIP)
	})
	t.Run("os-resolver-short-name", func(t *testing.T) {
		harness.WaitLookupIP(t, "echo", clusterIP)
	})
	t.Run("dial-by-fqdn", func(t *testing.T) {
		harness.WaitHostTCP(t, fqdn, 8080, "dns", "cluster-tcp:")
	})

	t.Run("set-dns-namespace", func(t *testing.T) {
		harness.WaitDNSA(t, port, priorityService, echoNamespaceIP)

		if err := live.Manager.SetDNSNamespace(harness.KubeContext(), "default"); err != nil {
			t.Fatal(err)
		}
		harness.WaitDNSA(t, port, "echo", clusterIP)
		harness.WaitDNSA(t, port, priorityService, defaultNamespaceIP)
		harness.WaitDNSA(t, port, fqdn, clusterIP)
		harness.WaitDNSA(t, port, aliasDomain, clusterIP)

		if err := live.Manager.SetDNSNamespace(harness.KubeContext(), harness.EchoNamespace); err != nil {
			t.Fatal(err)
		}
		harness.WaitDNSA(t, port, "echo", clusterIP)
		harness.WaitDNSA(t, port, priorityService, echoNamespaceIP)
	})

	updatedAliasIP := "192.0.2.10"
	t.Run("update-host-alias-while-connected", func(t *testing.T) {
		if err := live.Manager.SetHostAliases(harness.KubeContext(), []store.HostAliasSpec{
			{Domain: aliasDomain, IP: updatedAliasIP},
		}); err != nil {
			t.Fatal(err)
		}
		harness.WaitDNSA(t, port, aliasDomain, updatedAliasIP)
		harness.WaitDNSNotA(t, port, aliasDomain, clusterIP)
	})

	t.Run("host-alias-survives-reconnect", func(t *testing.T) {
		port = reconnectDNS(t, ctx, live.Manager)
		harness.WaitDNSA(t, port, aliasDomain, updatedAliasIP)
	})

	t.Run("clear-host-alias-while-connected", func(t *testing.T) {
		if err := live.Manager.SetHostAliases(harness.KubeContext(), nil); err != nil {
			t.Fatal(err)
		}
		harness.WaitDNSNotA(t, port, aliasDomain, updatedAliasIP)
		if aliases := live.Manager.HostAliases(harness.KubeContext()); len(aliases) != 0 {
			t.Fatalf("host aliases after clear = %#v", aliases)
		}
	})
}

func reconnectDNS(t *testing.T, ctx context.Context, manager *session.Manager) int {
	t.Helper()
	if err := manager.Disconnect(); err != nil {
		t.Fatal(err)
	}
	harness.AssertHelperIdle(t)

	connected := make(chan session.State, 1)
	failed := make(chan session.State, 1)
	manager.Subscribe(func(state session.State) {
		switch state.Phase {
		case session.PhaseConnected:
			select {
			case connected <- state:
			default:
			}
		case session.PhaseError:
			select {
			case failed <- state:
			default:
			}
		}
	})
	if err := manager.Connect(ctx, session.Request{
		Context: harness.KubeContext(), Namespace: harness.EchoNamespace,
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-connected:
	case state := <-failed:
		t.Fatalf("reconnected session failed: %s", state.Message)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	port, err := manager.DNSPort()
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func ensureDNSService(
	t *testing.T,
	ctx context.Context,
	client kubernetes.Interface,
	namespace, name string,
) string {
	t.Helper()
	services := client.CoreV1().Services(namespace)
	service, err := services.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		service, err = services.Create(ctx, &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: corev1.ServiceSpec{
				Ports: []corev1.ServicePort{{Name: "dns", Port: 80}},
			},
		}, metav1.CreateOptions{})
	}
	if err != nil {
		t.Fatalf("ensure DNS priority Service %s/%s: %v", namespace, name, err)
	}
	t.Cleanup(func() {
		_ = services.Delete(context.Background(), name, metav1.DeleteOptions{})
	})
	return service.Spec.ClusterIP
}

func TestTUNDNSGoneAfterDisconnect(t *testing.T) {
	harness.RequireE2E(t)
	ctx, cancel := harness.TestContext(t, 1*time.Minute)
	defer cancel()

	provider := harness.NewProvider(t)
	client := harness.KubeClient(t, provider)
	if err := harness.EnsureEchoWorkload(ctx, client); err != nil {
		t.Fatal(err)
	}
	clusterIP := harness.EchoServiceIP(t, ctx, client)

	live := harness.ConnectSession(t, ctx, session.Request{
		Context: harness.KubeContext(), Namespace: harness.EchoNamespace,
	}, nil)
	port, err := live.Manager.DNSPort()
	if err != nil {
		t.Fatal(err)
	}
	fqdn := "echo." + harness.EchoNamespace + ".svc.cluster.local"
	harness.WaitDNSA(t, port, fqdn, clusterIP)
	harness.WaitLookupIP(t, fqdn, clusterIP)

	if err := live.Manager.Disconnect(); err != nil {
		t.Fatal(err)
	}
	harness.AssertHelperIdle(t)
	harness.AssertClusterDNSGone(t, fqdn, clusterIP)

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		_, err := harness.ExchangeDNS(port, "udp", fqdn, dns.TypeA)
		if err != nil {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("DNS proxy on :%d still answering after disconnect", port)
}
