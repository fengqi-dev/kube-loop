package runtime

import (
	"context"
	"errors"
	"reflect"
	"slices"
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
			if err != nil && !errors.Is(err, errProcessClosed) {
				t.Fatalf("iteration %d: concurrent lifecycle operation failed: %v", i, err)
			}
		}
	}
}

func TestProcessDNSUpdatesCommitOnlyAfterPrivilegedApply(t *testing.T) {
	tests := []struct {
		name   string
		update func(*Process) error
	}{
		{
			name: "namespace",
			update: func(process *Process) error {
				return process.UpdateDNSNamespace(context.Background(), "payments")
			},
		},
		{
			name: "host aliases",
			update: func(process *Process) error {
				return process.UpdateHostAliases(context.Background(), []singbox.HostAlias{
					{Domain: "api.example.test", IP: "10.0.0.8"},
				})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			process := newDNSUpdateProcess(t)
			before := snapshotDNSUpdateState(process)
			applyErr := errors.New("helper update failed")
			process.updateDNS = func(context.Context, string, singbox.DNSMeta) error { return applyErr }
			if err := test.update(process); !errors.Is(err, applyErr) {
				t.Fatalf("update error = %v", err)
			}
			if after := snapshotDNSUpdateState(process); !reflect.DeepEqual(after, before) {
				t.Fatalf("failed update changed state:\nbefore = %#v\nafter = %#v", before, after)
			}

			observedBeforeCommit := false
			process.updateDNS = func(context.Context, string, singbox.DNSMeta) error {
				observedBeforeCommit = reflect.DeepEqual(snapshotDNSUpdateState(process), before)
				return nil
			}
			if err := test.update(process); err != nil {
				t.Fatal(err)
			}
			if !observedBeforeCommit {
				t.Fatal("local DNS state was committed before the privileged helper succeeded")
			}
			if after := snapshotDNSUpdateState(process); reflect.DeepEqual(after, before) {
				t.Fatal("successful update did not commit local DNS state")
			}
		})
	}
}

type dnsUpdateState struct {
	spec            singbox.SessionSpec
	resolverDomains []string
	search          []string
	domains         []string
	hosts           map[string]string
}

func newDNSUpdateProcess(t *testing.T) *Process {
	t.Helper()
	spec := validSessionSpec()
	spec.Hosts = []singbox.HostAlias{{Domain: "old.example.test", IP: "10.0.0.7"}}
	dnsMeta, err := spec.DNS()
	if err != nil {
		t.Fatal(err)
	}
	proxy := &dnsSearchProxy{search: slices.Clone(dnsMeta.Search), domains: slices.Clone(spec.ClusterDomains)}
	proxy.SetHostAliases(spec.Hosts)
	return &Process{
		done: make(chan struct{}), stopCh: make(chan struct{}), dnsProxy: proxy,
		spec: spec, resolverDomains: slices.Clone(dnsMeta.Domains),
	}
}

func snapshotDNSUpdateState(process *Process) dnsUpdateState {
	process.specMu.Lock()
	state := dnsUpdateState{
		spec: process.spec, resolverDomains: slices.Clone(process.resolverDomains),
	}
	state.spec.ClusterDomains = slices.Clone(state.spec.ClusterDomains)
	state.spec.Hosts = slices.Clone(state.spec.Hosts)
	proxy := process.dnsProxy
	process.specMu.Unlock()
	if proxy == nil {
		return state
	}
	proxy.mu.Lock()
	state.search = slices.Clone(proxy.search)
	state.domains = slices.Clone(proxy.domains)
	state.hosts = make(map[string]string, len(proxy.hosts))
	for name, ip := range proxy.hosts {
		state.hosts[name] = ip.String()
	}
	proxy.mu.Unlock()
	return state
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
