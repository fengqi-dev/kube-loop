package runtime

import (
	"context"
	"sync"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/singbox"
)

func TestProcessUpdateDNSNamespaceConcurrentClose(t *testing.T) {
	for i := range 100 {
		done := make(chan struct{})
		close(done)
		process := &Process{
			done:     done,
			stopCh:   make(chan struct{}),
			dnsProxy: &dnsSearchProxy{},
			spec:     validSessionSpec(),
			updateDNS: func(context.Context, string, singbox.DNSMeta) error {
				return nil
			},
		}

		start := make(chan struct{})
		errs := make(chan error, 2)
		var workers sync.WaitGroup
		workers.Add(2)
		go func() {
			defer workers.Done()
			<-start
			errs <- process.UpdateDNSNamespace(context.Background(), "kubeloop-e2e")
		}()
		go func() {
			defer workers.Done()
			<-start
			errs <- process.Close()
		}()
		close(start)
		workers.Wait()
		close(errs)

		for err := range errs {
			if err != nil {
				t.Fatalf("iteration %d: concurrent lifecycle operation failed: %v", i, err)
			}
		}
	}
}

func validSessionSpec() singbox.SessionSpec {
	return singbox.SessionSpec{
		ID:               "session-abc123",
		PodCIDRs:         []string{"10.244.0.0/16"},
		ServiceCIDRs:     []string{"10.96.0.0/12"},
		ClusterDNSServer: "10.96.0.10",
		ClusterDomains:   []string{"cluster.local"},
		BridgeHost:       "127.0.0.1",
		BridgePort:       1080,
		ControllerPort:   9090,
		ControllerSecret: "controller-secret-1234567890123456",
		DNSHost:          "127.0.0.1",
		DNSPort:          1053,
		PublicDNSPort:    53,
		TUNAddress:       "198.19.0.1/30",
		Namespace:        "default",
		TrafficPorts:     singbox.TrafficInboundPorts{Listen: 1081},
		TrafficPassword:  "traffic-password-1234567890123456",
	}
}
