package clusteradapter

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/fengqi-dev/kube-loop/internal/cluster"
	"github.com/fengqi-dev/kube-loop/internal/intercept"
)

type fakeProvider struct {
	service  *corev1.Service
	applied  *cluster.ServiceInterceptSnapshot
	restored bool
}

func (f *fakeProvider) GetService(
	context.Context, string, string, string,
) (*corev1.Service, error) {
	return f.service.DeepCopy(), nil
}

func (f *fakeProvider) ApplyServiceIntercept(
	_ context.Context,
	_ string,
	snapshot *cluster.ServiceInterceptSnapshot,
	_ string,
) error {
	ready := true
	name := "http"
	protocol := corev1.ProtocolTCP
	port := int32(8080)
	snapshot.HasEndpointSlices = true
	snapshot.EndpointSlices = []discoveryv1.EndpointSlice{{
		Ports: []discoveryv1.EndpointPort{{
			Name: &name, Protocol: &protocol, Port: &port,
		}},
		Endpoints: []discoveryv1.Endpoint{{
			Addresses: []string{"10.244.0.8"},
			Conditions: discoveryv1.EndpointConditions{
				Ready: &ready,
			},
		}},
	}}
	copySnapshot := *snapshot
	f.applied = &copySnapshot
	return nil
}

func (f *fakeProvider) RestoreServiceIntercept(
	context.Context, string, cluster.ServiceInterceptSnapshot,
) error {
	f.restored = true
	return nil
}

func (f *fakeProvider) CreatePreviewService(
	context.Context, string, cluster.PreviewServiceSnapshot, string,
) (*corev1.Service, error) {
	return nil, nil
}

func (f *fakeProvider) DeletePreviewService(
	context.Context, string, cluster.PreviewServiceSnapshot,
) error {
	return nil
}

func TestAdapterConvertsServiceAndOwnsRestoreSnapshot(t *testing.T) {
	provider := &fakeProvider{service: &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: "api", Namespace: "team",
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: "10.96.0.8",
			Selector:  map[string]string{"app": "api"},
			Ports: []corev1.ServicePort{{
				Name: "http", Protocol: corev1.ProtocolTCP, Port: 80,
			}},
		},
	}}
	adapter := New(provider)

	service, err := adapter.GetService(context.Background(), "dev", "team", "api")
	if err != nil {
		t.Fatal(err)
	}
	if service.ClusterIP != "10.96.0.8" || service.Ports[0].Protocol != intercept.ProtocolTCP {
		t.Fatalf("service = %#v", service)
	}

	lease, backends, err := adapter.ApplyServiceIntercept(
		context.Background(),
		"dev",
		intercept.ServiceInterceptRequest{
			Namespace: "team",
			Service:   "api",
			Selector:  service.Selector,
			GatewayIP: "10.244.0.2",
			ID:        "team/api",
			Ports: []intercept.InterceptPort{{
				Name: "http", Protocol: intercept.ProtocolTCP, ServicePort: 80, ListenPort: 20001,
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if provider.applied == nil || provider.applied.Ports[0].Protocol != corev1.ProtocolTCP {
		t.Fatalf("snapshot = %#v", provider.applied)
	}
	if len(backends) != 1 || backends[0].Address != "10.244.0.8" ||
		backends[0].Ports[0].Port != 8080 {
		t.Fatalf("backends = %#v", backends)
	}
	if err := lease.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !provider.restored {
		t.Fatal("restore was not delegated through the lease")
	}
}
