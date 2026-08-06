package clusteradapter

import (
	"context"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/cluster"
	"github.com/fengqi-dev/kube-loop/internal/portfwd"
)

type fakeProvider struct {
	pods     []cluster.PodInfo
	services []cluster.ServiceInfo
}

func (f *fakeProvider) ListPods(
	context.Context, string, string,
) ([]cluster.PodInfo, error) {
	return f.pods, nil
}

func (f *fakeProvider) ListServices(
	context.Context, string, string,
) ([]cluster.ServiceInfo, error) {
	return f.services, nil
}

func (f *fakeProvider) ResolveServiceBackend(
	context.Context, string, string, string, int32,
) (string, uint16, error) {
	return "api-0", 8080, nil
}

func (f *fakeProvider) StartPodPortForward(
	context.Context, string, string, string, uint16, uint16,
) (cluster.PortForward, error) {
	return nil, nil
}

func TestResolveRoutedTargets(t *testing.T) {
	adapter := New(&fakeProvider{
		pods: []cluster.PodInfo{{
			Name: "api-0", Namespace: "team", IP: "10.244.0.8",
		}},
		services: []cluster.ServiceInfo{{
			Name: "api", Namespace: "team", ClusterIP: "10.96.0.8",
		}},
	})

	pod, err := adapter.ResolveRoutedTarget(context.Background(), portfwd.Request{
		Context: "dev", Namespace: "team", Kind: portfwd.KindPod,
		Name: "api-0", RemotePort: 8080,
	})
	if err != nil {
		t.Fatal(err)
	}
	if pod != "10.244.0.8:8080" {
		t.Fatalf("pod target = %q", pod)
	}

	service, err := adapter.ResolveRoutedTarget(context.Background(), portfwd.Request{
		Context: "dev", Namespace: "team", Kind: portfwd.KindService,
		Name: "api", RemotePort: 80,
	})
	if err != nil {
		t.Fatal(err)
	}
	if service != "10.96.0.8:80" {
		t.Fatalf("service target = %q", service)
	}
}
