package kubernetes_test

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	controlplanekubernetes "github.com/fengqi-dev/kube-loop/internal/controlplane/kubernetes"
	portforwardservice "github.com/fengqi-dev/kube-loop/internal/controlplane/portforwardapi/service"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficmodel"
)

type staticClientProvider struct {
	client kubernetes.Interface
}

func (p staticClientProvider) ClientFor(authorization.Subject) (kubernetes.Interface, error) {
	return p.client, nil
}

func TestServiceResolverRequiresEveryRequestedPortToBeAuthoritative(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.Service{
		Name: "api", Namespace: "development",
		Spec: corev1.ServiceSpec{
			ClusterIP: "10.96.0.20",
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 80, Protocol: corev1.ProtocolTCP},
				{Name: "dns", Port: 53, Protocol: corev1.ProtocolUDP},
			},
		},
	})
	resolver, err := controlplanekubernetes.NewServiceResolver(staticClientProvider{client: client})
	if err != nil {
		t.Fatal(err)
	}
	service, err := resolver.ResolveService(
		context.Background(),
		controlplaneapi.Identity{Subject: "user"},
		"development",
		"api",
		[]trafficmodel.Port{{ServicePort: 53, Protocol: "udp"}, {ServicePort: 80, Protocol: "tcp"}},
	)
	if err != nil || service.ClusterIP != "10.96.0.20" || len(service.Ports) != 2 ||
		service.Ports[0].Name != "dns" {
		t.Fatalf("resolved Service=%#v err=%v", service, err)
	}

	if _, err := resolver.ResolveService(
		context.Background(),
		controlplaneapi.Identity{Subject: "user"},
		"development",
		"api",
		[]trafficmodel.Port{{ServicePort: 53, Protocol: "tcp"}},
	); err == nil {
		t.Fatal("mismatched protocol was accepted")
	}
}

func TestPortForwardResolverResolvesPodAndService(t *testing.T) {
	objects := []runtime.Object{
		&corev1.Pod{
			Name: "api-0", Namespace: "development",
			Status: corev1.PodStatus{Phase: corev1.PodRunning, PodIP: "10.244.1.7"},
		},
		&corev1.Service{
			Name: "api", Namespace: "development",
			Spec: corev1.ServiceSpec{
				ClusterIP: "10.96.0.20",
				Ports:     []corev1.ServicePort{{Port: 8443, Protocol: corev1.ProtocolTCP}},
			},
		},
	}
	resolver, err := controlplanekubernetes.NewPortForwardResolver(
		staticClientProvider{client: fake.NewSimpleClientset(objects...)},
	)
	if err != nil {
		t.Fatal(err)
	}
	identity := controlplaneapi.Identity{Subject: "user"}
	pod, err := resolver.Resolve(
		context.Background(),
		identity,
		"development",
		portforwardservice.Spec{
			Kind: "pod", Name: "api-0", Protocol: "tcp", RemotePort: 8080,
		},
	)
	if err != nil || pod.Address() != "10.244.1.7:8080" {
		t.Fatalf("pod target = %#v err = %v", pod, err)
	}
	service, err := resolver.Resolve(
		context.Background(),
		identity,
		"development",
		portforwardservice.Spec{
			Kind: "service", Name: "api", Protocol: "tcp", RemotePort: 8443,
		},
	)
	if err != nil || service.Address() != "10.96.0.20:8443" {
		t.Fatalf("service target = %#v err = %v", service, err)
	}
	if _, err := resolver.Resolve(context.Background(), identity, "development", portforwardservice.Spec{
		Kind: "service", Name: "api", Protocol: "udp", RemotePort: 8443,
	}); err == nil {
		t.Fatal("mismatched protocol was accepted")
	}
}

func TestContainerResolverSelectsAndValidatesContainer(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Pod{
			Name: "single", Namespace: "development",
			Spec:   corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
		&corev1.Pod{
			Name: "multiple", Namespace: "development",
			Spec: corev1.PodSpec{Containers: []corev1.Container{
				{Name: "app"}, {Name: "sidecar"},
			}},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
	)
	resolver, err := controlplanekubernetes.NewContainerResolver(
		staticClientProvider{client: client},
	)
	if err != nil {
		t.Fatal(err)
	}
	identity := controlplaneapi.Identity{Subject: "user"}
	container, err := resolver.ResolveContainer(
		context.Background(),
		identity,
		"development",
		"single",
		"",
	)
	if err != nil || container != "app" {
		t.Fatalf("container = %q, err = %v", container, err)
	}
	if _, err := resolver.ResolveContainer(context.Background(), identity, "development", "multiple", ""); err == nil {
		t.Fatal("multiple containers were accepted without an explicit name")
	}
	if _, err := resolver.ResolveContainer(
		context.Background(),
		identity,
		"development",
		"multiple",
		"missing",
	); err == nil {
		t.Fatal("unknown container was accepted")
	}
}
